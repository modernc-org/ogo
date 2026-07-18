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

    cc -std=gnu11 -Wall -Wextra -I <thisdir> prog.c -lpthread

`-std=gnu11` rather than `-std=c11`: the shim needs POSIX (`nanosleep`,
pthreads), which strict ISO mode hides. The emitted code itself does not.
