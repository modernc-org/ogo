# counter

Print an incrementing counter over the serial terminal.

[`main.ogo`](main.ogo) prints an integer that increases by one every 500 ms,
forever, using `println`. It is the companion to [`../blink`](../blink):
where blink drives a pin and produces no output, counter exercises the serial
path (`println` -> `printf` over the P2's programming UART), so you can watch it
work in `ogo run`'s terminal.

## Build

```sh
ogo build main.ogo
```

## Run

Compile, load, and open the interactive terminal to watch the count:

```sh
ogo run main.ogo
```

You should see `0`, `1`, `2`, ... one per line every half second. Press
`Ctrl-]` or `Ctrl-Z` to exit the terminal.

The terminal runs at 230400 baud (`ogo`'s default, matching the baud the
emitted program prints at). To load without a terminal and connect your own,
build first and use the loader directly:

```sh
ogo build main.ogo
ogo loadp2 -b 230400 main.binary
```
