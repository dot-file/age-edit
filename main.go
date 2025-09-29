package main

import (
	"bytes"
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
	"github.com/carlmjohnson/crockford"
	"github.com/spf13/pflag"
)

const (
	defaultTempDirPrefixLinux = "/dev/shm/"

	filePerm         = 0o600
	fileReadOnlyPerm = 0o400
	tempDirPerm      = 0o700

	armorEnvVar          = "AGE_EDIT_ARMOR"
	encryptedFileEnvVar  = "AGE_EDIT_ENCRYPTED_FILE"
	identitiesFileEnvVar = "AGE_EDIT_IDENTITIES_FILE"
	readOnlyEnvVar       = "AGE_EDIT_READ_ONLY"
	tempDirPrefixEnvVar  = "AGE_EDIT_TEMP_DIR"
	warnEnvVar           = "AGE_EDIT_WARN"

	version = "0.10.0"
)

var (
	editorEnvVars = []string{"AGE_EDIT_EDITOR", "VISUAL", "EDITOR"}
)

type encryptError struct {
	err      error
	tempFile string
}

func (e *encryptError) Error() string {
	return fmt.Sprintf("encryption failed: %v", e.err)
}

// Handle both armored and binary age files transparently.
func wrapDecrypt(r io.Reader, identities ...age.Identity) (io.Reader, error) {
	buffer := make([]byte, len(armor.Header))

	// Check if the input starts with an armor header.
	n, err := io.ReadFull(r, buffer)
	if err != nil && !errors.Is(err, io.EOF) && n < len(armor.Header) {
		return nil, fmt.Errorf("failed to read header: %v", err)
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
		var w io.Writer = out

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
	buf := make([]byte, 0, 8)
	buf = crockford.AppendRandom(crockford.Lower, buf)
	return string(buf)
}

func getRoot(path string) string {
	return strings.TrimSuffix(path, ".age")
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
		f, err := os.OpenFile(path, os.O_RDWR, 0600)

		if err != nil {
			return true, fmt.Errorf("can't write to file %q", path)
		}

		f.Close()
	}

	return true, nil
}

func loadIdentities(path string) ([]age.Identity, []age.Recipient, error) {
	var identities []age.Identity
	var recipients []age.Recipient

	identityData, err := os.ReadFile(path)
	if err != nil {
		return identities, recipients, fmt.Errorf("failed to read identities file: %v", err)
	}

	identityCount := 0
	for _, line := range strings.Split(string(identityData), "\n") {
		line := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		identityCount++

		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return identities, recipients, fmt.Errorf("failed to parse private key number %d: %v", identityCount, err)
		}

		identities = append(identities, identity)
		recipients = append(recipients, identity.Recipient())
	}

	if len(identities) == 0 {
		return identities, recipients, fmt.Errorf("no identities found in file")
	}

	return identities, recipients, nil
}

func edit(idsPath, encPath, tempDirPrefix string, armor bool, editor string, readOnly bool) (tempDir string, err error) {
	var exists bool
	exists, err = checkAccess(encPath, readOnly)
	if err != nil {
		return
	}

	identities, recipients, err := loadIdentities(idsPath)
	if err != nil {
		return
	}

	currentUser, err := user.Current()
	if err != nil {
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	userDir := fmt.Sprintf("age-edit-%s@%s", currentUser.Username, hostname)
	subdir := randomID()
	tempDir = filepath.Join(tempDirPrefix, userDir, subdir)
	err = os.MkdirAll(tempDir, tempDirPerm)
	if err != nil {
		return
	}

	rootname := getRoot(encPath)
	tempFile := filepath.Join(tempDir, filepath.Base(rootname))

	if exists {
		if err = decryptToFile(encPath, tempFile, identities...); err != nil {
			return
		}
	}

	if readOnly {
		if err = os.Chmod(tempFile, fileReadOnlyPerm); err != nil {
			return
		}
	}

	cmd := exec.Command(editor, tempFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return
	}

	if !readOnly {
		if err = encryptToFile(tempFile, encPath, armor, recipients...); err != nil {
			err = &encryptError{err: err, tempFile: tempFile}
			return
		}
	}

	return
}

func parseBool(s string) (bool, error) {
	if s == "" {
		return false, nil
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

	b, err := parseBool(val)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", armorEnvVar, val)
	}

	return b, nil
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

	b, err := parseBool(val)
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

func cli() int {
	if err := lockMemory(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	defaultArmorVal, err := defaultArmor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	defaultReadOnlyVal, err := defaultReadOnly()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	defaultWarnVal, err := defaultWarn()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	flag := pflag.NewFlagSet("age-edit", pflag.ContinueOnError)
	armored := flag.BoolP(
		"armor",
		"a",
		defaultArmorVal,
		fmt.Sprintf("write an armored age file (%v)", armorEnvVar),
	)
	editorFlag := flag.StringP(
		"editor",
		"e",
		defaultEditor(),
		fmt.Sprintf("command to use for editing the encrypted file (%v)", strings.Join(editorEnvVars, ", ")),
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
		fmt.Sprintf("warn if the editor exits after less than a number seconds (%v, 0 to disable)", warnEnvVar),
	)

	flag.Usage = func() {
		message := fmt.Sprintf(
			`Usage: %s [options] [[identities-file] encrypted-file]

Options:
%s
An identities file and an encrypted file, given in the arguments or the environment variables, are required. Default values are read from environment variables with a built-in fallback. Boolean environment variables accept 0, 1, true, false, yes, no.
`,
			filepath.Base(os.Args[0]),
			// Merge "(default ...)" with our own parentheticals.
			strings.ReplaceAll(flag.FlagUsages(), ") (", ", "),
		)

		fmt.Fprint(os.Stderr, message)
	}

	if err := flag.Parse(os.Args[1:]); err != nil {
		if err == pflag.ErrHelp {
			return 0
		}

		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	if *showVersion {
		fmt.Println(version)

		return 0
	}

	if flag.NArg() > 2 {
		fmt.Fprintln(
			os.Stderr,
			"Error: too many arguments",
		)
		return 2
	}

	filename := os.Getenv(encryptedFileEnvVar)
	keyPath := os.Getenv(identitiesFileEnvVar)

	if flag.NArg() == 1 {
		filename = flag.Arg(0)
	} else if flag.NArg() == 2 {
		keyPath = flag.Arg(0)
		filename = flag.Arg(1)
	}

	if keyPath == "" || filename == "" {
		fmt.Fprintln(
			os.Stderr,
			"Error: need an identities file and an encrypted file",
		)
		return 2
	}

	start := int(time.Now().Unix())

	tempDir, err := edit(keyPath, filename, *tempDirPrefix, *armored, *editorFlag, *readOnly)
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

		if encErr, ok := err.(*encryptError); ok {
			fmt.Fprintf(
				os.Stderr,
				"Press <Enter> to delete temporary file %q\n",
				encErr.tempFile,
			)
			_, _ = fmt.Scanln()
		}

		return 1
	}

	return 0
}

func main() {
	os.Exit(cli())
}
