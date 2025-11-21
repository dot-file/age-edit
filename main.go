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
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/anmitsu/go-shlex"
	"github.com/carlmjohnson/crockford"
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
	memlockEnvVar        = "AGE_EDIT_MEMLOCK"
	readOnlyEnvVar       = "AGE_EDIT_READ_ONLY"
	tempDirPrefixEnvVar  = "AGE_EDIT_TEMP_DIR"
	warnEnvVar           = "AGE_EDIT_WARN"

	version = "0.12.0"
)

var (
	editorEnvVars = []string{"AGE_EDIT_EDITOR", "VISUAL", "EDITOR"}
)

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

func edit(idsPath, encPath, tempDirPrefix string, armor bool, readOnly bool, command string, arg ...string) (string, error) {
	exists, err := checkAccess(encPath, readOnly)
	if err != nil {
		return "", err
	}

	identities, recipients, err := loadIdentities(idsPath)
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
	tempDir := filepath.Join(tempDirPrefix, userDir, subdir)

	err = os.MkdirAll(tempDir, tempDirPerm)
	if err != nil {
		return tempDir, err
	}

	rootname := getRoot(encPath)
	tempFile := filepath.Join(tempDir, filepath.Base(rootname))

	if exists {
		if err = decryptToFile(encPath, tempFile, identities...); err != nil {
			return tempDir, err
		}
	}

	beforeSum, err := checksumFile(tempFile)
	if err != nil {
		return tempDir, err
	}

	if readOnly {
		if err = os.Chmod(tempFile, fileReadOnlyPerm); err != nil {
			return tempDir, err
		}
	}

	cmd := exec.CommandContext(context.Background(), editor, tempFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return tempDir, err
	}

	if !readOnly {
		afterSum, err := checksumFile(tempFile)
		if err != nil {
			return tempDir, &saveError{err: err, tempFile: tempFile}
		}

		if !bytes.Equal(beforeSum, afterSum) {
			if err = encryptToFile(tempFile, encPath, armor, recipients...); err != nil {
				return tempDir, &saveError{err: err, tempFile: tempFile}
			}
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

func defaultArmor() (bool, error) {
	val := os.Getenv(armorEnvVar)

	b, err := parseBool(val, false)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", armorEnvVar, val)
	}

	return b, nil
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

func defaultReadOnly() (bool, error) {
	val := os.Getenv(readOnlyEnvVar)

	b, err := parseBool(val, false)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", readOnlyEnvVar, val)
	}

	return b, nil
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

func defaultMemlock() (bool, error) {
	val := os.Getenv(memlockEnvVar)

	b, err := parseBool(val, true)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", memlockEnvVar, val)
	}

	return b, nil
}

func defaultArg(envVar string) (string, string) {
	value := os.Getenv(envVar)

	helpDefault := ""
	if value != "" {
		helpDefault = fmt.Sprintf(", default %q", value)
	}

	return value, helpDefault
}

func cli() int {
	encryptedFile, encryptedFileHelpDefault := defaultArg(encryptedFileEnvVar)
	identitiesFile, identitiesFileHelpDefault := defaultArg(identitiesFileEnvVar)

	defaultArmorVal, err := defaultArmor()
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

	defaultMemlockVal, err := defaultMemlock()
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
	commandFlag := flag.StringP(
		"command",
		"c",
		defaultCommand(),
		fmt.Sprintf("command to run (overrides editor, %v)", commandEnvVar),
	)
	editorFlag := flag.StringP(
		"editor",
		"e",
		defaultEditor(),
		fmt.Sprintf("editor executable to run (%v)", strings.Join(editorEnvVars, ", ")),
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
		fmt.Sprintf("warn if the editor exits after less than a number of seconds (%v, 0 to disable)", warnEnvVar),
	)
	noMemlock := flag.BoolP(
		"no-memlock",
		"M",
		!defaultMemlockVal,
		fmt.Sprintf("disable mlockall(2) that prevents swapping (negated %v)", memlockEnvVar),
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

	//nolint:mnd
	if flag.NArg() == 1 {
		encryptedFile = flag.Arg(0)
	} else if flag.NArg() == 2 {
		identitiesFile = flag.Arg(0)
		encryptedFile = flag.Arg(1)
	}

	if identitiesFile == "" || encryptedFile == "" {
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

	editorCommand := *editorFlag
	editorArgs := []string{}

	if *commandFlag != "" {
		args, err := shlex.Split(*commandFlag, true)

		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: failed to split command")
			os.Exit(exitBadUsage)
		}

		editorCommand = args[0]
		editorArgs = args[1:]
	}

	start := int(time.Now().Unix())

	tempDir, err := edit(identitiesFile, encryptedFile, *tempDirPrefix, *armored, *readOnly, editorCommand, editorArgs...)
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
