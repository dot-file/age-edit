//go:build unix

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
)

func TestSignalSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "age-edit-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	writeFileInTempDir := func(name, content string) (string, error) {
		path := filepath.Join(tempDir, name)

		return path, os.WriteFile(path, []byte(content), 0o600)
	}

	buildInTempDir := func(pkg, output string) (string, error) {
		path := filepath.Join(tempDir, output)
		cmd := exec.Command("go", "build", "-o", path, pkg)

		return path, cmd.Run()
	}

	// Set up an identity and an identity file.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	idFilePath, err := writeFileInTempDir("id", identity.String())
	if err != nil {
		t.Fatal(err)
	}

	plainFilePath, err := writeFileInTempDir("foo", "initial")
	if err != nil {
		t.Fatal(err)
	}

	encFilePath, err := writeFileInTempDir("foo.age", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := encryptToFile(plainFilePath, encFilePath, true, identity.Recipient()); err != nil {
		t.Fatal(err)
	}

	// Build the binaries.
	ageEditPath, err := buildInTempDir(".", "age-edit")
	if err != nil {
		t.Fatalf("failed to build age-edit binary: %v", err)
	}

	testEditPath, err := buildInTempDir("./test/edit", "test-edit")
	if err != nil {
		t.Fatalf("failed to build ./test/edit binary: %v", err)
	}

	// Run the age-edit binary with test/edit as the editor.
	errChan := make(chan error)
	go func() {
		cmd := exec.Command(
			ageEditPath,
			"--editor", testEditPath,
			"--no-memlock",
			"--temp-dir", tempDir,
			idFilePath,
			encFilePath,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		errChan <- cmd.Run()
	}()

	// Poll the encrypted file for "phase1".
	// We have about a second while the script sleeps.
	success := false
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)

		// Try to decrypt.
		decFilePath, err := writeFileInTempDir("dec", "")
		if err != nil {
			t.Fatal(err)
		}

		err = decryptToFile(encFilePath, decFilePath, identity)
		if err == nil {
			content, err := os.ReadFile(decFilePath)
			if err != nil {
				t.Fatal(err)
			}

			if bytes.Contains(content, []byte("phase1")) {
				success = true

				break
			}
		}
	}

	if !success {
		t.Error("Did not detect intermediate save triggered by SIGUSR1")
	}

	// Wait for age-edit to finish.
	err = <-errChan
	if err != nil {
		t.Errorf("age-edit failed: %v", err)
	}

	// Verify the final state of the file.
	decFilePath, err := writeFileInTempDir("dec", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := decryptToFile(encFilePath, decFilePath, identity); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(decFilePath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(content, []byte("phase2")) {
		t.Error("Final save did not contain phase2")
	}
}
