# Settings

Every setting of the `vlatte` command, as `vlatte --help` lists it: its flag, its environment variable and its key in a `.vlatterc` file. See [Configuration](/run/configuration) for the order sources apply in and how values are written.

Servers made in Go read the same settings from a `.vlatterc` file and `VLATTE_*` variables, except those of the command only, such as `https` and `admin`: see [Configuration](/run/configuration#sources-in-tests).

<<< @/.vitepress/generated/settings.txt{txt}
