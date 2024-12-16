package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
)

const (
	filePerm      = 0o600
	tempDirPerm   = 0o700
	tempDirPrefix = "/dev/shm"
	version       = "0.4.0"
)

type encryptError struct {
	err      error
	tempFile string
}

func (e *encryptError) Error() string {
	return fmt.Sprintf("encryption failed: %v", e.err)
}

// Returns a reader that can handle both armored and binary age files.
func wrapDecrypt(r io.Reader, identities ...age.Identity) (io.Reader, error) {
	// Check if the input starts with an armor header.
	seeker, ok := r.(io.Seeker)
	if !ok {
		return nil, fmt.Errorf("input must be seekable")
	}

	// Read enough bytes to check for the armor header.
	header := make([]byte, len(armor.Header))
	_, err := io.ReadFull(r, header)
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %v", err)
	}

	_, err = seeker.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek: %v", err)
	}

	isArmored := string(header) == armor.Header

	if isArmored {
		armoredReader := armor.NewReader(r)
		decryptedReader, err := age.Decrypt(armoredReader, identities...)
		if err != nil {
			return nil, fmt.Errorf("armored decryption failed: %v", err)
		}

		return decryptedReader, nil
	}

	// Try binary decryption.
	decryptedReader, err := age.Decrypt(r, identities...)
	if err != nil {
		return nil, fmt.Errorf("binary decryption failed: %v", err)
	}

	return decryptedReader, nil
}

func decrypt(in, out string, identities ...age.Identity) error {
	inFile, err := os.Open(in)
	if err != nil {
		return err
	}
	defer inFile.Close()

	outFile, err := os.Create(out)
	if err != nil {
		return err
	}
	defer outFile.Close()

	d, err := wrapDecrypt(inFile, identities...)
	if err != nil {
		return err
	}

	_, err = io.Copy(outFile, d)
	return err
}

func encrypt(in, out string, recipients ...age.Recipient) error {
	inFile, err := os.Open(in)
	if err != nil {
		return err
	}
	defer inFile.Close()

	outFile, err := os.Create(out)
	if err != nil {
		return err
	}
	defer outFile.Close()

	armorWriter := armor.NewWriter(outFile)
	defer armorWriter.Close()

	encryptWriter, err := age.Encrypt(armorWriter, recipients...)
	if err != nil {
		return err
	}
	defer encryptWriter.Close()

	_, err = io.Copy(encryptWriter, inFile)
	return err
}

func edit(keyPath, encrypted, editor string, readOnly bool) (tempDir string, err error) {
	var exists bool
	exists, err = checkAccess(encrypted, readOnly)
	if err != nil {
		return
	}

	// Load the private keys.
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read keyfile: %v", err)
	}

	var identities []age.Identity
	var recipients []age.Recipient

	keyCount := 0
	for _, line := range strings.Split(string(keyData), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		keyCount++

		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return "", fmt.Errorf("failed to parse private key number %d: %v", keyCount, err)
		}

		identities = append(identities, identity)
		recipients = append(recipients, identity.Recipient())
	}

	if len(identities) == 0 {
		return "", fmt.Errorf("no identities found in keyfile")
	}

	currentUser, err := user.Current()
	if err != nil {
		return
	}

	tempDir = filepath.Join(tempDirPrefix, currentUser.Username+"-age-edit")
	err = os.MkdirAll(tempDir, tempDirPerm)
	if err != nil {
		return
	}

	rootname := getRoot(encrypted)
	var tempFile *os.File
	tempFile, err = os.CreateTemp(tempDir, "*"+filepath.Base(rootname))
	if err != nil {
		return
	}
	tempFile.Close()

	// This check from the Tcl version is probably unnecessary.
	if err = checkPermissions(tempDir, tempDirPerm); err != nil {
		return
	}
	if err = checkPermissions(tempFile.Name(), filePerm); err != nil {
		return
	}

	if exists {
		if err = decrypt(encrypted, tempFile.Name(), identities...); err != nil {
			return
		}
	}

	cmd := exec.Command(editor, tempFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return
	}

	if !readOnly {
		if err = encrypt(tempFile.Name(), encrypted, recipients...); err != nil {
			err = &encryptError{err: err, tempFile: tempFile.Name()}
			return
		}
	}

	return
}

func cli() int {
	editorFlag := flag.String("editor", "", "the editor to use")
	readOnly := flag.Bool("ro", false, "read-only mode -- all changes will be discarded")
	showVersion := flag.Bool("v", false, "report the program version and exit")
	warn := flag.Int("warn", 0, "warn if the editor exits after less than X seconds")

	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"Usage: %s [options] keyfile encrypted-file\n",
			filepath.Base(os.Args[0]),
		)
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return 0
	}

	if flag.NArg() < 2 {
		flag.Usage()

		return 2
	}

	editor := *editorFlag
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}

	if *warn < 0 {
		fmt.Fprintln(os.Stderr, "Error: the argument to -warn can't be negative")
		return 2
	}

	keyPath := flag.Arg(0)
	filename := flag.Arg(1)

	start := int(time.Now().Unix())

	tempDir, err := edit(keyPath, filename, editor, *readOnly)
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}

	if *warn > 0 && int(time.Now().Unix())-start <= int(*warn) {
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
				"Press <enter> to delete temporary file %q\n",
				encErr.tempFile,
			)
			fmt.Scanln()
		}

		return 1
	}

	return 0
}

func main() {
	os.Exit(cli())
}

func checkPermissions(filename string, perm os.FileMode) error {
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}

	actualPerm := info.Mode().Perm()
	if actualPerm != perm {
		return fmt.Errorf("wrong permissions on %q: %o instead of %o", filename, actualPerm, perm)
	}

	return nil
}

func getRoot(encrypted string) string {
	ext := filepath.Ext(encrypted)

	if ext == ".age" {
		return strings.TrimSuffix(encrypted, ext)
	}

	return encrypted
}

func checkAccess(encrypted string, readOnly bool) (bool, error) {
	_, err := os.Stat(encrypted)

	if err != nil && os.IsNotExist(err) {
		if readOnly {
			return false, fmt.Errorf("%q doesn't exist; won't attempt to create it in read-only mode", encrypted)
		}

		return false, nil
	}

	f, err := os.Open(encrypted)
	if err != nil {
		return true, fmt.Errorf("can't read from file %q", encrypted)
	}
	f.Close()

	// If not in read-only mode, try to open for writing.
	// We don't want writing to fail later, after the user edits the file.
	if !readOnly {
		f, err := os.OpenFile(encrypted, os.O_RDWR, 0600)

		if err != nil {
			return true, fmt.Errorf("can't write to file %q", encrypted)
		}

		f.Close()
	}

	return true, nil
}
