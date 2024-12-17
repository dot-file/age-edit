package main

import (
	"os"
	"testing"

	"filippo.io/age"
)

func TestCheckAccess(t *testing.T) {
	// Create a temporary file to test against.
	tempFile, err := os.CreateTemp("", "test-file")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	tests := []struct {
		path     string
		readOnly bool
		expectOk bool
	}{
		// File exists and is readable.
		{tempFile.Name(), true, true},
		// File does not exist in read-only mode.
		{"nonexistent-file", true, false},
		// File does not exist, not read-only mode.
		{"nonexistent-file", false, true},
	}

	for _, tt := range tests {
		_, err := checkAccess(tt.path, tt.readOnly)
		if (err == nil) != tt.expectOk {
			t.Errorf("checkAccess(%q, readOnly=%v) = %v, expected %v", tt.path, tt.readOnly, err == nil, tt.expectOk)
		}
	}
}

func TestEncryptAndDecryptToFile(t *testing.T) {
	testData := "Hello, world!\n"

	// Create a temporary file for the input.
	inputFile, err := os.CreateTemp("", "input")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputFile.Name())
	_, _ = inputFile.WriteString(testData)
	inputFile.Close()

	// Create a temporary file for the encrypted and decrypted the output.
	encryptedFile, err := os.CreateTemp("", "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encryptedFile.Name())

	decryptedFile, err := os.CreateTemp("", "decrypted")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(decryptedFile.Name())

	// Generate an age key pair for testing.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	recipient := identity.Recipient()

	// Test encryption.
	err = encryptToFile(inputFile.Name(), encryptedFile.Name(), true, recipient)
	if err != nil {
		t.Errorf("encryptToFile() failed: %v", err)
	}

	// Test decryption.
	err = decryptToFile(encryptedFile.Name(), decryptedFile.Name(), identity)
	if err != nil {
		t.Errorf("decryptToFile() failed: %v", err)
	}

	// Compare decrypted content with the original.
	decryptedContent, _ := os.ReadFile(decryptedFile.Name())
	if string(decryptedContent) != testData {
		t.Errorf("Decrypted content mismatch: got %q, but expected %q", decryptedContent, testData)
	}
}

func TestGetRoot(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt.age", "file.txt"},
		{"example.age", "example"},
		{"example.odt", "example.odt"},
		{"no-ext", "no-ext"},
	}

	for _, tt := range tests {
		result := getRoot(tt.input)

		if result != tt.expected {
			t.Errorf("getRoot(%q) is %q, expected %q", tt.input, result, tt.expected)
		}
	}
}


func TestLoadIdentities(t *testing.T) {
	corruptedKey := "AGE-SECRET-KEY-1XXXXXXXXXX1234567890abcdefghijklmnopqrstuvwxyz"
	validKey := "AGE-SECRET-KEY-150E3TFLT765WC7X9E2Y6KAN2XA7NE4DN0XVCR4ATTFQK6GSXCGVS3KS7MS"

	tests := []struct {
		content  string
		expected int
		hasError bool
	}{
		// A single valid key.
		{validKey + "\n", 1, false},
		// A single valid key without a line feed.
		{validKey, 1, false},
		// Multiple valid keys.
		{validKey + "\n" + validKey + "\n", 2, false},
		// An obviously invalid key.
		{"invalid-key\n", 0, true},
		// A corrupted key.
		{corruptedKey + "\n", 0, true},
		// Ignore comments and blank lines.
		{"# Comment\n \n\n" + validKey + "\n", 1, false},
		// An empty file.
		{"", 0, true},
	}

	for _, tt := range tests {
		tempFile, err := os.CreateTemp("", "identities")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tempFile.Name())

		_, err = tempFile.WriteString(tt.content)
		if err != nil {
			t.Fatal(err)
		}
		tempFile.Close()

		ids, recs, err := loadIdentities(tempFile.Name())

		if tt.hasError && err == nil {
			t.Errorf("loadIdentities(%q) expected error, got none", tt.content)
		}

		if !tt.hasError && len(ids) != tt.expected {
			t.Errorf("loadIdentities(%q) returned %d identities, expected %d", tt.content, len(ids), tt.expected)
		}

		if len(ids) != len(recs) {
			t.Errorf("loadIdentities(%q) returned mismatched identities and recipients", tt.content)
		}
	}
}
