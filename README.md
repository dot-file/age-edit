# This is a fork of age-edit [original repository](https://github.com/dbohdan/age-edit) where it is packaged for Nix using gomod2nix and flakes.
Commits from upstream are automatically [merged](https://github.com/dot-file/age-edit/blob/master/.github/workflows/update-gomod2nix.yml) and if they contain changes for go dependencies, new gomod2nix hashes are [generated](https://github.com/dot-file/age-edit/blob/master/.github/workflows/update-gomod2nix.yml).

## Usage
- You can run age-edit with a single command (don't forget to put two dashes before age-edit arguments):
  ```nix
  nix run github:dot-file/age-edit -- --version
  ```
- Or install it with flakes:
  ```nix
  {
    # add age-edit flake to your inputs
    inputs.age-edit = {
      url = "github:dbohdan/age-edit";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # ensure that age-edit is an allowed argument to the outputs function
    outputs = { self, nixpkgs, age-edit }: {
      nixosConfigurations.yourHostName = nixpkgs.lib.nixosSystem {
        modules = [
          ({ pkgs, ... }: {
            # add the age-edit overlay to make the package available through pkgs
            nixpkgs.overlays = [ age-edit.overlays.default ];

            # install the package globally
            environment.systemPackages = [ pkgs.age-edit ];
          })
        ];
      };
    };
  }
  ```

The following is the original README:
# age-edit

age-edit is an editor wrapper for files encrypted with [age](https://github.com/FiloSottile/age).
age-edit is designed primarily for Linux and uses `/dev/shm/` by default.
However, it supports and is automatically tested on FreeBSD, macOS, NetBSD, OpenBSD, and Windows.
On those systems, it is left to the user to choose a temporary directory prefix.

## How age-edit works

When you run age-edit with a identities (private-keys) file and an encrypted file, it performs the following steps:

1. Decrypts the contents of the age-encrypted file to a temporary file using one of the identities (private keys).
2. Opens an editor on the temporary file.
   (The default editor is determined by the environment variables `AGE_EDIT_EDITOR`, [`VISUAL`, and `EDITOR`](https://unix.stackexchange.com/questions/4859/visual-vs-editor-what-s-the-difference) with `vi` as a fallback, but it can be any editor, e.g., LibreOffice.)
3. Waits for the editor to exit.
4. Encrypts the temporary file with public keys derived from the private keys.
   The encrypted file can be optionally "armored": stored as ASCII text in the [PEM](https://en.wikipedia.org/wiki/Privacy-Enhanced_Mail) format.
5. Finally, deletes the temporary file.

In other words, age-edit implements
[a](https://wiki.tcl-lang.org/39218)
"[with](https://www.python.org/dev/peps/pep-0343/)"
[pattern](https://clojuredocs.org/clojure.core/with-open).

If any error occurs between step 4 and 5, age-edit waits for you to press Enter to delete the temporary file.
This gives you an opportunity to save the edited version of the file so you don't lose your edits.

age-edit is beta-quality software.

## Requirements

### Build

- Go 1.21
- Optional: [Task](https://taskfile.dev/) (go-task) 3.28

### Runtime

- Optional: a high limit on locked memory.
  This allows age-edit to use a function that prevents it from being swapped out.
  See the [documentation](https://github.com/dbohdan/pago#memory-locking) for the pago password manager for instructions on configuring the limit.
- Optional: a temporary filesystem mounted on `/dev/shm/`.
  It is usually present on Linux with glibc.

## Installation

```shell
go install dbohdan.com/age-edit@latest
```

## Usage

```none
Usage: age-edit [options] [[identities] encrypted]

Arguments:
  identities              identities file path (AGE_EDIT_IDENTITIES_FILE)
  encrypted               encrypted file path (AGE_EDIT_ENCRYPTED_FILE)

Options:
  -a, --armor             write an armored age file (AGE_EDIT_ARMOR)
  -e, --editor string     command to use for editing the encrypted file
(AGE_EDIT_EDITOR, VISUAL, EDITOR, default "vi")
  -M, --no-memlock        disable mlockall(2) that prevents swapping (negated
AGE_EDIT_MEMLOCK)
  -r, --read-only         make the temporary file read-only and discard all
changes (AGE_EDIT_READ_ONLY)
  -t, --temp-dir string   temporary directory prefix (AGE_EDIT_TEMP_DIR,
default "/dev/shm/")
  -V, --version           report the program version and exit
  -w, --warn int          warn if the editor exits after less than a number
seconds (AGE_EDIT_WARN, 0 to disable)

An identities file and an encrypted file, given in the arguments or the
environment variables, are required. Default values are read from environment
variables with a built-in fallback. Boolean environment variables accept 0, 1,
true, false, yes, no.
```

## Using age-edit with pago

You can use age-edit with a private key stored in [pago](https://github.com/dbohdan/pago) or a similar password manager.
Invoke age-edit like this:

```shell
# Bash
# `pago show secret.key` outputs the private key to stdout.
age-edit -a <(pago show secret.key) secret.txt
```

```fish
# fish shell
# `pago show secret.key` outputs the private key to stdout.
# `--fifo` avoids writing the secret key to a temporary file.
age-edit -a (pago show secret.key | psub --fifo) secret.txt
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

rm "$decompressed"
```

## Security and other considerations

The age identities (private keys) from the keyfile are kept in memory while the encrypted file is being edited.
On POSIX systems, the program locks its memory pages using [`mlockall`](https://pubs.opengroup.org/onlinepubs/9799919799/functions/mlockall.html) to prevent being swapped to disk.
The process memory may be saved in unencrypted swap if the system is suspended to disk.
No attempt to prevent the swapping of the process is made on non-POSIX systems like Windows.

The decrypted contents of the file are stored by default in the directory `/dev/shm/age-edit-${username}@${hostname}/abcd0123/`, where `abcd0123` is random.
You can change this to `/custom/path/age-edit-${username}@${hostname}/abcd0123/`.
Other programs run by the same user can access the decrypted file contents.
Note that `/dev/shm/` can be swapped out when swap is enabled.

age-edit doesn't work with multi-document editors.

## License

MIT.
