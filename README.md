# age-edit

age-edit is an editor wrapper for files encrypted with [age](https://github.com/FiloSottile/age).
age-edit is made primarily for Linux and uses `/dev/shm/` by default.
However, it supports and is automatically tested on FreeBSD, macOS, NetBSD, OpenBSD, and Windows.
It is left to the user to choose a temporary directory prefix on those systems.

Here is how age-edit works.
When you run age-edit with a private-keys file and an encrypted file as arguments, it performs the following steps:

1. Decrypt the contents of the file encrypted with age to a temporary file using one of the private keys.
2. Run an editor on the temporary file.
   (The default editor is [`VISUAL` or `EDITOR`](https://unix.stackexchange.com/questions/4859/visual-vs-editor-what-s-the-difference), but it can be, e.g., LibreOffice.)
3. Wait for the editor to exit.
4. Encrypt the temporary file with public keys derived from the private keys.
   The encrypted file can be optionally "armored": stored as ASCII text in the [PEM](https://en.wikipedia.org/wiki/Privacy-Enhanced_Mail) format.
5. Finally, delete the temporary file.

In other words, age-edit implements
[a](https://wiki.tcl-lang.org/39218)
"[with](https://www.python.org/dev/peps/pep-0343/)"
[pattern](https://clojuredocs.org/clojure.core/with-open).

If any error occurs between step 4 and 5, age-edit waits for you to press Enter to delete the temporary file.
This gives you an opportunity to save the edited version of the file so you don't lose your edits.

age-edit is beta-quality software.

## Dependencies

### Build

- Go 1.21
- Optional: [Task](https://taskfile.dev/) (go-task) 3.28

### Runtime

- Optional: a temporary filesystem mounted on `/dev/shm/`.
  It is usually present on Linux with glibc.

## Installation

```shell
go install dbohdan.com/age-edit@latest
```

## Usage

```none
Usage: age-edit [options] identities encrypted-file

Options:
  -a, --armor             write an armored age file
  -e, --editor string     command to use for editing the encrypted file
  -r, --read-only         make the temporary file read-only and discard all changes
  -t, --temp-dir string   temporary directory prefix (default "/dev/shm/")
  -V, --version           report the program version and exit
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
"${VISUAL:-${EDITOR:-vi}}" "$decompressed"
zstd -7 --long < "$decompressed" > "$1"
```

## Security and other considerations

The age identities (private keys) from the keyfile are kept in memory while the encrypted file is being edited.
On POSIX systems, the program locks its memory pages using [`mlockall`](https://pubs.opengroup.org/onlinepubs/9799919799/functions/mlockall.html) to prevent being swapped to disk.
The process memory may be saved in unencrypted swap if the system is suspended to disk.
No attempt to prevent the swapping of the process is made on non-POSIX systems like Windows.

The decrypted contents of the file is stored by default in the directory `/dev/shm/age-edit-${USER}/abcd0123/`, where `abcd0123` is random.
You can change this to `/custom/path/age-edit-${USER}/abcd0123/`.
Other programs run by the same user can access the decrypted file contents.
`/dev/shm/` can be swapped out.

age-edit doesn't work with multi-document editors.

## License

MIT.
