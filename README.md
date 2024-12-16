# age-edit

age-edit is an editor wrapper for age-encrypted files.
It is made primarily for Linux because it uses `/dev/shm/`.

This is how age-edit works:

1. It decrypts the contents of a file encrypted with age to a temporary file using private keys.
2. It runs an editor on the temporary file.
  (The editor is [`VISUAL` or `EDITOR`](https://unix.stackexchange.com/questions/4859/visual-vs-editor-what-s-the-difference) by default, but it can be, e.g., LibreOffice.)
3. It waits for the editor to exit.
4. It encrypts the temporary file with public keys derived from the private keys.
   The encrypted file is [armored](https://en.wikipedia.org/wiki/Privacy-Enhanced_Mail).
5. Finally, age-edit deletes the temporary file.

In other words, age-edit implements
[a](https://wiki.tcl-lang.org/39218)
"[with](https://www.python.org/dev/peps/pep-0343/)"
[pattern](https://clojuredocs.org/clojure.core/with-open).

age-edit is beta-quality software.

## Dependencies

### Build

- Go 1.21

### Runtime

- A temporary filesystem mounted at `/dev/shm/`.
  It is present by default on Linux with glibc.

## Installation

```shell
go install github.com/dbohdan/age-edit@master
```

## Usage

```
Usage: age-edit [options] keyfile encrypted-file
  -editor string
    	the editor to use
  -ro
    	read-only mode -- all changes will be discarded
  -v	report the program version and exit
  -warn int
    	warn if the editor exits after less than X seconds
```

## Security and other considerations

The age identities (private keys) from the keyfile are kept in memory while the encrypted file is being edited.
This memory can be accessed by the user's other programs or read from the swap if age-edit is swapped out.
The decrypted contents of the file is stored on a temporary filesystem in RAM at `/dev/shm/${USER}-age-edit`.
Other programs run by the same user can access it there, and it can also be swapped out.

age-edit doesn't work with multi-document editors.

## License

MIT.
