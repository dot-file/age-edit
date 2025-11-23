package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/anmitsu/go-shlex"
	"github.com/carlmjohnson/crockford"
	"github.com/gofrs/flock"
	"github.com/spf13/pflag"
	"lukechampine.com/blake3"
)

const (
	digestSize     = 32
	randomIDLength = 8

	exitOK       = 0
	exitError    = 1
	exitBadUsage = 2

	cliMaxArgs = 2

	defaultTempDirPrefixLinux = "/dev/shm/"

	filePerm         = 0o600
	fileReadOnlyPerm = 0o400
	tempDirPerm      = 0o700

	armorEnvVar          = "AGE_EDIT_ARMOR"
	commandEnvVar        = "AGE_EDIT_COMMAND"
	encryptedFileEnvVar  = "AGE_EDIT_ENCRYPTED_FILE"
	identitiesFileEnvVar = "AGE_EDIT_IDENTITIES_FILE"
	lockEnvVar           = "AGE_EDIT_LOCK"
	memlockEnvVar        = "AGE_EDIT_MEMLOCK"
	readOnlyEnvVar       = "AGE_EDIT_READ_ONLY"
	tempDirPrefixEnvVar  = "AGE_EDIT_TEMP_DIR"
	warnEnvVar           = "AGE_EDIT_WARN"

	version = "0.13.0"
)

var (
	editorEnvVars = []string{"AGE_EDIT_EDITOR", "VISUAL", "EDITOR"}
)

type config struct {
	idsPath       string
	encPath       string
	tempDirPrefix string

	armor    bool
	lock     bool
	readOnly bool

	command string
	args    []string
}

type saveError struct {
	err      error
	tempFile string
}

func (e *saveError) Error() string {
	return fmt.Sprintf("encryption failed: %v", e.err)
}

// Handle both armored and binary age files transparently.
func wrapDecrypt(r io.Reader, identities ...age.Identity) (io.Reader, error) {
	buffer := make([]byte, len(armor.Header))

	// Check if the input starts with an armor header.
	n, err := io.ReadFull(r, buffer)
	if err != nil && !errors.Is(err, io.EOF) && n < len(armor.Header) {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	armored := string(buffer[:n]) == armor.Header
	r = io.MultiReader(bytes.NewReader(buffer[:n]), r)

	if armored {
		return age.Decrypt(armor.NewReader(r), identities...)
	}

	return age.Decrypt(r, identities...)
}

func withFiles(inputPath, outputPath string, action func(in io.Reader, out io.Writer) error) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return action(in, out)
}

func decryptToFile(inputPath, outputPath string, identities ...age.Identity) error {
	return withFiles(inputPath, outputPath, func(in io.Reader, out io.Writer) error {
		d, err := wrapDecrypt(in, identities...)
		if err != nil {
			return err
		}

		_, err = io.Copy(out, d)

		return err
	})
}

func encryptToFile(inputPath, outputPath string, armored bool, recipients ...age.Recipient) error {
	return withFiles(inputPath, outputPath, func(in io.Reader, out io.Writer) error {
		w := out

		if armored {
			armorWriter := armor.NewWriter(out)
			defer armorWriter.Close()

			w = armorWriter
		}

		encryptWriter, err := age.Encrypt(w, recipients...)
		if err != nil {
			return err
		}
		defer encryptWriter.Close()

		_, err = io.Copy(encryptWriter, in)

		return err
	})
}

func randomID() string {
	buf := make([]byte, 0, randomIDLength)
	buf = crockford.AppendRandom(crockford.Lower, buf)

	return string(buf)
}

func getRoot(path string) string {
	return strings.TrimSuffix(path, ".age")
}

func checksumFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return the checksum of an empty file.
			h := blake3.New(digestSize, nil)

			return h.Sum(nil), nil
		}

		return nil, err
	}
	defer f.Close()

	h := blake3.New(digestSize, nil)
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

func checkAccess(path string, readOnly bool) (bool, error) {
	_, err := os.Stat(path)

	if err != nil && os.IsNotExist(err) {
		if readOnly {
			return false, fmt.Errorf("%q does not exist; won't attempt to create it in read-only mode", path)
		}

		return false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return true, fmt.Errorf("can't read from file %q", path)
	}

	f.Close()

	// If not in read-only mode, try to open for writing.
	// We don't want writing to fail later, after the user edits the file.
	if !readOnly {
		f, err := os.OpenFile(path, os.O_RDWR, filePerm)
		if err != nil {
			return true, fmt.Errorf("can't write to file %q", path)
		}

		f.Close()
	}

	return true, nil
}

func loadIdentities(path string) ([]age.Identity, []age.Recipient, error) {
	identityData, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read identities file: %w", err)
	}

	identityCount := 0
	lines := strings.Split(string(identityData), "\n")
	identities := make([]age.Identity, 0, len(lines))
	recipients := make([]age.Recipient, 0, len(lines))

	for _, line := range strings.Split(string(identityData), "\n") {
		line := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		identityCount++

		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse private key number %d: %w", identityCount, err)
		}

		identities = append(identities, identity)
		recipients = append(recipients, identity.Recipient())
	}

	if len(identities) == 0 {
		return identities, recipients, errors.New("no identities found in file")
	}

	return identities, recipients, nil
}

func edit(cfg config) (string, error) {
	exists, err := checkAccess(cfg.encPath, cfg.readOnly)
	if err != nil {
		return "", err
	}

	identities, recipients, err := loadIdentities(cfg.idsPath)
	if err != nil {
		return "", err
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	userDir := fmt.Sprintf("age-edit-%s@%s", currentUser.Username, hostname)
	subdir := randomID()
	tempDir := filepath.Join(cfg.tempDirPrefix, userDir, subdir)

	err = os.MkdirAll(tempDir, tempDirPerm)
	if err != nil {
		return tempDir, err
	}

	rootname := getRoot(cfg.encPath)
	tempFile := filepath.Join(tempDir, filepath.Base(rootname))

	encLock := flock.New(cfg.encPath)

	//nolint:nestif
	if exists {
		if cfg.lock && !cfg.readOnly {
			locked, err := encLock.TryLock()
			if err != nil {
				return tempDir, fmt.Errorf("failed to acquire lock: %w", err)
			}

			if !locked {
				return tempDir, errors.New("encrypted file is locked")
			}

			defer func() {
				_ = encLock.Unlock()
			}()
		}

		if err := decryptToFile(cfg.encPath, tempFile, identities...); err != nil {
			return tempDir, err
		}
	}

	beforeSum, err := checksumFile(tempFile)
	if err != nil {
		return tempDir, err
	}

	if cfg.readOnly {
		if err := os.Chmod(tempFile, fileReadOnlyPerm); err != nil {
			return tempDir, err
		}
	}

	var mu sync.Mutex

	saveChanges := func() error {
		mu.Lock()
		defer mu.Unlock()

		currentSum, err := checksumFile(tempFile)
		if err != nil {
			return err
		}

		if !bytes.Equal(beforeSum, currentSum) {
			if err = encryptToFile(tempFile, cfg.encPath, cfg.armor, recipients...); err != nil {
				return err
			}

			beforeSum = currentSum
		}

		return nil
	}

	if !cfg.readOnly {
		stop := handleSignals(saveChanges)
		defer stop()
	}

	fullArgs := append([]string{}, cfg.args...)
	fullArgs = append(fullArgs, tempFile)

	cmd := exec.CommandContext(context.Background(), cfg.command, fullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return tempDir, err
	}

	if !cfg.readOnly {
		if err := saveChanges(); err != nil {
			return tempDir, &saveError{err: err, tempFile: tempFile}
		}
	}

	return tempDir, nil
}

func parseBool(s string, fallback bool) (bool, error) {
	if s == "" {
		return fallback, nil
	}

	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true, nil

	case "0", "false", "no":
		return false, nil

	default:
		return false, fmt.Errorf("invalid boolean value: %q", s)
	}
}

func defaultArg(envVar string) (string, string) {
	value := os.Getenv(envVar)

	helpDefault := ""
	if value != "" {
		helpDefault = fmt.Sprintf(", default %q", value)
	}

	return value, helpDefault
}

func defaultBool(envVar string, fallback bool) (bool, error) {
	val := os.Getenv(envVar)

	b, err := parseBool(val, fallback)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", envVar, val)
	}

	return b, nil
}

func defaultArmor() (bool, error) {
	return defaultBool(armorEnvVar, false)
}

func defaultCommand() string {
	return os.Getenv(commandEnvVar)
}

func defaultEditor() string {
	for _, envVar := range editorEnvVars {
		value := os.Getenv(envVar)
		if value != "" {
			return value
		}
	}

	return "vi"
}

func defaultLock() (bool, error) {
	return defaultBool(lockEnvVar, true)
}

func defaultMemlock() (bool, error) {
	return defaultBool(memlockEnvVar, true)
}

func defaultReadOnly() (bool, error) {
	return defaultBool(readOnlyEnvVar, false)
}

func defaultTempDirPrefix() string {
	prefix := os.Getenv(tempDirPrefixEnvVar)
	if prefix == "" {
		prefix = defaultTempDirPrefixLinux
	}

	return prefix
}

func defaultWarn() (int, error) {
	val := os.Getenv(warnEnvVar)
	if val == "" {
		return 0, nil
	}

	i, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value for %s: %q", warnEnvVar, val)
	}

	return i, nil
}

func cli() int {
	encryptedFileDefault, encryptedFileHelpDefault := defaultArg(encryptedFileEnvVar)
	identitiesFileDefault, identitiesFileHelpDefault := defaultArg(identitiesFileEnvVar)

	defaultArmorVal, err := defaultArmor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultLockVal, err := defaultLock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultMemlockVal, err := defaultMemlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultReadOnlyVal, err := defaultReadOnly()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultWarnVal, err := defaultWarn()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	flag := pflag.NewFlagSet("age-edit", pflag.ContinueOnError)

	armored := flag.BoolP(
		"armor",
		"a",
		defaultArmorVal,
		fmt.Sprintf("write an armored age file (%v)", armorEnvVar),
	)
	command := flag.StringP(
		"command",
		"c",
		defaultCommand(),
		fmt.Sprintf("command to run (overrides editor, %v)", commandEnvVar),
	)
	editor := flag.StringP(
		"editor",
		"e",
		defaultEditor(),
		fmt.Sprintf("editor executable to run (%v)", strings.Join(editorEnvVars, ", ")),
	)
	noLock := flag.BoolP(
		"no-lock",
		"L",
		!defaultLockVal,
		fmt.Sprintf("do not lock encrypted file (negated %v)", lockEnvVar),
	)
	noMemlock := flag.BoolP(
		"no-memlock",
		"M",
		!defaultMemlockVal,
		fmt.Sprintf("disable mlockall(2) that prevents swapping (negated %v)", memlockEnvVar),
	)
	readOnly := flag.BoolP(
		"read-only",
		"r",
		defaultReadOnlyVal,
		fmt.Sprintf("make the temporary file read-only and discard all changes (%v)", readOnlyEnvVar),
	)
	showVersion := flag.BoolP(
		"version",
		"V",
		false,
		"report the program version and exit",
	)
	tempDirPrefix := flag.StringP(
		"temp-dir",
		"t",
		defaultTempDirPrefix(),
		fmt.Sprintf("temporary directory prefix (%v)", tempDirPrefixEnvVar),
	)
	warn := flag.IntP(
		"warn",
		"w",
		defaultWarnVal,
		fmt.Sprintf("warn if the editor exits after less than a number of seconds (0 to disable, %v)", warnEnvVar),
	)

	flag.Usage = func() {
		message := fmt.Sprintf(
			`Usage: %s [options] [[identities] encrypted]

Arguments:
  identities              identities file path (%s%s)
  encrypted               encrypted file path (%s%s)

Options:
%s
An identities file and an encrypted file, given in the arguments or the environment variables, are required. Default values are read from environment variables with a built-in fallback. Boolean environment variables accept 0, 1, true, false, yes, no.
`,
			filepath.Base(os.Args[0]),
			identitiesFileEnvVar,
			identitiesFileHelpDefault,
			encryptedFileEnvVar,
			encryptedFileHelpDefault,
			// Merge "(default ...)" with our own parentheticals.
			strings.ReplaceAll(flag.FlagUsages(), ") (", ", "),
		)

		fmt.Fprint(os.Stderr, message)
	}
	if err := flag.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return exitOK
		}

		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	if *showVersion {
		fmt.Println(version)

		return exitOK
	}

	if flag.NArg() > cliMaxArgs {
		fmt.Fprintln(
			os.Stderr,
			"Error: too many arguments",
		)

		return exitBadUsage
	}

	cfg := config{
		idsPath:       identitiesFileDefault,
		encPath:       encryptedFileDefault,
		tempDirPrefix: *tempDirPrefix,

		armor:    *armored,
		lock:     !*noLock,
		readOnly: *readOnly,

		command: *editor,
		args:    []string{},
	}

	//nolint:mnd
	if flag.NArg() == 1 {
		cfg.encPath = flag.Arg(0)
	} else if flag.NArg() == 2 {
		cfg.idsPath = flag.Arg(0)
		cfg.encPath = flag.Arg(1)
	}

	if cfg.encPath == "" || cfg.idsPath == "" {
		fmt.Fprintln(
			os.Stderr,
			"Error: need an identities file and an encrypted file",
		)

		return exitBadUsage
	}

	if !*noMemlock {
		if err := lockMemory(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v. You may need to increase the limit on locked memory. Pass --no-memlock to suppress this error.\n", err)

			return exitError
		}
	}

	if *command != "" {
		args, err := shlex.Split(*command, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: failed to split command")
			os.Exit(exitBadUsage)
		}

		cfg.command = args[0]
		cfg.args = args[1:]
	}

	start := int(time.Now().Unix())

	tempDir, err := edit(cfg)
	if tempDir != "" {
		// Remove the "age-edit-..." directory if empty
		// after removing the temporary file and the random subdirectory.
		defer os.Remove(filepath.Dir(tempDir))
		defer os.RemoveAll(tempDir)
	}

	if *warn > 0 && int(time.Now().Unix())-start <= *warn {
		fmt.Fprintf(
			os.Stderr,
			"Warning: editor exited after less than %d second(s)\n",
			*warn,
		)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		var saveErr *saveError
		if errors.As(err, &saveErr) {
			fmt.Fprintf(
				os.Stderr,
				"Press <Enter> to delete temporary file %q\n",
				saveErr.tempFile,
			)

			_, _ = fmt.Scanln()
		}

		return exitError
	}

	return exitOK
}

func main() {
	os.Exit(cli())
}
