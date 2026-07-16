# blink

The "hello, world" of microcontrollers: blink an on-board LED.

[`main.ogo`](main.ogo) toggles P2 smart pin 56 (an on-board LED on the
Parallax P2-EDGE module) on and off every 200 ms, forever. It is the first
end-to-end example — `.ogo` source all the way to a running program on the
board — and uses nothing but the pure-Go `ogo` toolchain: the OctoGo frontend,
the in-process flexcc C backend, and the in-process loadp2 loader.

## Build

Produce a P2 binary (`main.binary`) without touching the hardware:

```sh
ogo build main.ogo
```

## Run

Compile, load onto a connected board, and open an interactive terminal:

```sh
ogo run main.ogo
```

Or build first and load explicitly (no terminal — the LED just blinks):

```sh
ogo build main.ogo
ogo loadp2 -b 230400 main.binary
```

The loader auto-detects the serial port (e.g. `/dev/ttyUSB0`). Once loaded, the
LED starts blinking immediately.
