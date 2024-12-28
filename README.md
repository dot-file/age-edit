# age-edit

age-edit is an editor wrapper for files encrypted with [age](https://github.com/FiloSottile/age).
It is made primarily for Linux and uses `/dev/shm/` by default.

This is how age-edit works:

1. It decrypts the contents of a file encrypted with age to a temporary file using private keys.
2. It runs an editor on the temporary file.
  (The default editor is [`VISUAL` or `EDITOR`](https://unix.stackexchange.com/questions/4859/visual-vs-editor-what-s-the-difference) but it can be, e.g., LibreOffice.)
3. It waits for the editor to exit.
4. It encrypts the temporary file with public keys derived from the private keys.
   The encrypted file can be optionally "armored": stored as ASCII text in the [PEM](https://en.wikipedia.org/wiki/Privacy-Enhanced_Mail) format.
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

- Optional: a temporary filesystem mounted on `/dev/shm/`.
  It is usually present on Linux with glibc.

## Installation

```shell
go install dbohdan.com/age-edit@latest
```

## Usage

```
Usage: age-edit [options] identities encrypted-file

Options:
  -a, --armor             write armored age file
  -e, --editor string     command to use for editing the encrypted file
  -r, --read-only         discard all changes
  -t, --temp-dir string   temporary directory prefix (default "/dev/shm/")
  -v, --version           report the program version and exit
  -w, --warn int          warn if the editor exits after less than a number seconds (zero to disable)
```

## Editing compressed files

With a shell script like this, you can edit compressed files using age-edit.
Give age-edit the path to the shell script as `--editor`.

```shell
#! /bin/sh
set -eu

cd "$(dirname "$(realpath "$1")")"
decompressed=$(dirname "$1")/dc-$(basename "$1")

# Decompress if not empty.
if [ -s "$1" ]; then
    zstd -d < "$1" > "$decompressed"
fi
"${VISUAL:-${EDITOR:-vim}}" "$decompressed"
zstd -7 --long < "$decompressed" > "$1"
```

## Security and other considerations

The age identities (private keys) from the keyfile are kept in memory while the encrypted file is being edited.
This memory can be accessed by the user's other programs or read from the swap if age-edit is swapped out.
The decrypted contents of the file is stored in the directory `${USER}/age-edit/` at a temporary location.
This location defaults to `/dev/shm/`.
Other programs run by the same user can access it there, and it can also be swapped out.

age-edit doesn't work with multi-document editors.

## License

MIT.
