complete -c age-edit -s a -l armor -d 'Write armored age file'
complete -c age-edit -s e -l editor -d 'Command to use for editing the encrypted file' -r
complete -c age-edit -s r -l read-only -d 'Discard all changes'
complete -c age-edit -s v -l version -d 'Report the program version and exit'
complete -c age-edit -s t -l temp-dir -d 'Temporary directory prefix' -r
complete -c age-edit -s w -l warn -d 'Warn if editor exits after less than N seconds' -r

# Complete files for both arguments.
complete -c age-edit -n "__fish_is_nth_token 1" -F
complete -c age-edit -n "__fish_is_nth_token 2" -F
