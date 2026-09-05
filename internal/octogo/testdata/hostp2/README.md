# Host shim for the Propeller 2 intrinsics

Emitted C targets the P2 and calls flexprop intrinsics that do not exist on a
development machine. This directory provides just enough of `propeller2.h` to
compile and *run* that output off-target, so a change to the emitter can be
checked by observing behaviour rather than only by reading the generated code.

It is a test fixture, not part of the compiler or of any shipped program.

| P2 | host stand-in |
| --- | --- |
| cogs (`_cogstart`, 8 of them) | detached pthreads, same limit |
| hardware locks (`_locknew`, 16) | mutexes, same limit |
| `_waitx` (yield the hub bus) | short `nanosleep` |
| `_waitms` | no-op |

The limits are enforced so the exhaustion paths -- "out of cogs", "out of
hardware locks" -- are reachable in a test rather than only on real silicon.

Compile emitted C against it with:

    cc -std=gnu11 -fwrapv -Wall -Wextra -I <thisdir> prog.c -lpthread

`-std=gnu11` rather than `-std=c11`: the shim needs POSIX (`nanosleep`,
pthreads), which strict ISO mode hides. The emitted code itself does not.

`-fwrapv` because signed integer overflow is undefined in C but WRAPPING in
Go, and the P2 wraps too (its soft 64-bit routines were checked on the board).
The shim exists to model the target off-board, so it must model that wrapping:
without the flag the host gcc exploits the overflow and computes a different
answer than both Go and the hardware -- an overflowing `x * K % 3` folded to a
value that matches neither. This is fidelity to the target, not a workaround;
the emitted C run on the real P2 is already correct. (Found by the smith oracle
at a wide seed sweep.)
