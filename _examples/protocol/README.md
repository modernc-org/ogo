# protocol — a framed binary protocol over the serial line

The host sends a frame, the board answers with one. It is the shape a device
speaking to a program on a PC wants, and it exists here because the two things that
make it work are not guessable from the language.

A frame is a start byte, a length, that many payload bytes, and a checksum over the
length and the payload:

```
AA  len  b0 b1 ... b(len-1)  sum
```

The reply is the same frame with every payload byte incremented, so a host can tell
an answer from an echo. A bad checksum is dropped rather than answered, and the
parser resynchronises on the next start byte.

## Running it

```sh
ogo build _examples/protocol
```

Then load it and send it frames. It answers three and stops. With the loader's
terminal you are typing bytes at it, so a shell that can emit them is easiest:

```sh
ogo loadp2 -t -b 230400 protocol.binary
```

Sending `AA 02 10 0A 1C` gets `AA 02 11 0B 1E` back. On a P2-EDGE, all three of
these round-trip byte for byte:

| sent | received |
| :--- | :--- |
| `AA 02 10 0A 1C` | `AA 02 11 0B 1E` |
| `AA 01 FF 00` | `AA 01 00 01` |
| `AA 03 01 02 03 09` | `AA 03 02 03 04 0C` |

The second one is worth a look: `0xFF` increments to `0x00`, so the reply carries a
NUL byte. `printf` cannot put one on a wire and neither can anything else here —
`p2.WriteByte` is the only path that writes a byte as it stands.

## The two things that are not guessable

**A whole cog does nothing but read.** Nothing is buffered behind `p2.ReadByte`: a
byte arriving while the cog is elsewhere is gone, and at 230400 baud a byte is 43
microseconds, which is less than one `printf`. Measured on a P2-EDGE, a loop reading
into an array caught all eight bytes of a frame; the same loop with one `printf` per
byte caught four of them, and said nothing about the other four.

**The handover is a ring, not a channel.** A channel here is a rendezvous — the send
parks the reader until the far side arrives to take the value, which is exactly the
pause the line does not wait through. Measured the same way, a reader handing bytes
over a channel lost every byte of the next two frames while the worker printed the
answer to the first. A single-producer/single-consumer ring never blocks the
producer: the reader only stores and bumps `head`, the worker only reads and bumps
`tail`, and each index is written by one cog and read by the other.

Those two together are why this is an example rather than a paragraph.
