# Shell Commands

Short reference for builtin commands

| Command | Options | Short description |
| --- | --- | --- |
| `true` | none | Exit with status `0`. |
| `false` | none | Exit with status `1`. |
| `echo [ARG ...]` | none | Print arguments separated by spaces, then newline. |
| `cat [FILE ...]` | `-` (read stdin) | Print files; with no args, read stdin. |
| `head [FILE ...]` | `-n N` (lines), `-c N` (bytes), `-q`/`--quiet`/`--silent` (no headers), `-v` (force headers) | Print first 10 lines of each FILE; with no FILE or `-`, read stdin. |
| `test EXPR` / `[ EXPR ]` | `-e`/`-f`/`-d`/`-s`/`-r`/`-w`/`-x`/`-L` (file tests), `-n`/`-z`/`=`/`!=` (strings), `-eq`/`-ne`/`-lt`/`-gt`/`-le`/`-ge` (integers), `-nt`/`-ot`/`-ef` (file comparison), `!`/`-a`/`-o` (logic) | Evaluate conditional expression; exit 0 (true) or 1 (false). |
| `exit [N]` | `N` (status code) | Exit the shell with `N` (default: last status). |
| `break [N]` | `N` (loop levels) | Break current loop, or `N` enclosing loops. |
| `continue [N]` | `N` (loop levels) | Continue current loop, or `N` enclosing loops. |
