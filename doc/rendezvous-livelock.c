/*
 * A livelock in a lock-guarded rendezvous between two Propeller 2 Cogs, and its
 * fix. Reduced from OctoGo's channel implementation, which hung on hardware for
 * any program with a few channels and a few goroutines.
 *
 * This file was previously called flexcc-fcache-bug.c and blamed flexspin's
 * FCACHE for the hang. That was wrong, and the correction is the point of
 * keeping it. FCACHE was not miscompiling anything; it was making the polling
 * loop fast enough to expose a bug in the loop itself.
 *
 * The bug. Both sides poll by calling _locktry every turn:
 *
 *	while (1) {
 *		if (_locktry(c->lock)) {	// asks for the lock unconditionally
 *			if (c->full) { ... }
 *			_lockrel(c->lock);
 *		}
 *		_waitx(1);
 *	}
 *
 * A cog running that loop re-takes the hub lock faster than the cog on the other
 * side can win it. Both stay live, neither progresses. Nothing is miscompiled and
 * nothing is stale; the two sides simply never both get what they need.
 *
 * What proves it is a livelock rather than a stale read: vary only the backoff.
 * Measured on a P2-EDGE against flexprop v7.6.11 and v7.7.0 alike, FCACHE on,
 * four cells and four Cogs.
 *
 *	_waitx(1)	stops after 2 of 4
 *	_waitx(8)	stops after 3 of 4
 *	_waitx(64)	runs to completion
 *
 * A load being served from stale Cog RAM could not care how long the loop waits
 * before retrying. The emitted OctoGo rendezvous needed 256 rather than 64, which
 * is the same story with a different constant.
 *
 * The fix, and what this file does by default: read the volatile flag before
 * asking for the lock, and ask only when the read says there is plausibly
 * something to do. The check inside the lock is still the authoritative one, so
 * the outer read is a hint that may be wrong in either direction -- a false
 * positive costs one acquire and release, a false negative costs one more turn.
 * The backoff stays at a single cycle. Raising the backoff instead also works,
 * but it paces the symptom rather than removing the contention, costs latency on
 * every rendezvous, and leaves the threshold to be rediscovered by whichever
 * program next beats it.
 *
 * Build and run on a P2:
 *
 *	flexcc -2            -o ok.binary  rendezvous-livelock.c   # completes
 *	flexcc -2 -DLIVELOCK -o bad.binary rendezvous-livelock.c   # hangs
 *	loadp2 -t -b 230400 bad.binary
 *
 * Expected, and what the default build prints:
 *
 *	begin
 *	1
 *	2
 *	3
 *	4
 *	done
 *
 * With -DLIVELOCK, and only on hardware: "begin", "1", "2", then nothing, with
 * both sides still spinning.
 *
 * The shape of the program matters as much as the loop. One cell and one Cog does
 * not reproduce it; four do. The cells are four separate locals because that is
 * what the compiler emits, one per channel -- a single array in their place is a
 * weaker reduction, being fixed by __builtin_alloca where the real program is
 * not.
 *
 * With thanks to Wuerfel_21 on the Parallax forum, who said it was a bug in the
 * logic exposed by faster timings, and was right.
 */

#include <propeller2.h>
#include <stdio.h>

typedef struct {
	int lock;
	volatile int full;  /* set by the producer, polled by the consumer */
	volatile int taken; /* bumped by the consumer, polled by the producer */
	volatile int val;
} cell;

/* WHEN(x) is the pre-test that keeps a polling loop off the lock. Defining
   LIVELOCK removes it, restoring the loop that hangs. */
#ifdef LIVELOCK
#define WHEN(x) 1
#else
#define WHEN(x) (x)
#endif

static long stacks[4][256];

/* produce deposits v, then waits for the consumer to take that value. */
static void produce(cell *c, int v)
{
	int mine = 0;

	while (1) {
		if (WHEN(!c->full) && _locktry(c->lock)) {
			if (!c->full) {
				mine = c->taken;
				c->val = v;
				c->full = 1;
				_lockrel(c->lock);
				break;
			}
			_lockrel(c->lock);
		}
		_waitx(1);
	}
	while (1) {
		int done = 0;

		if (WHEN(c->taken != mine) && _locktry(c->lock)) {
			done = c->taken != mine;
			_lockrel(c->lock);
		}
		if (done) {
			return;
		}
		_waitx(1);
	}
}

static int consume(cell *c)
{
	while (1) {
		if (WHEN(c->full) && _locktry(c->lock)) {
			if (c->full) {
				int v = c->val;

				c->full = 0;
				c->taken++;
				_lockrel(c->lock);
				return v;
			}
			_lockrel(c->lock);
		}
		_waitx(1);
	}
}

typedef struct {
	cell *c;
	int v;
} args;

static args argblk[4];

static void tramp(void *p)
{
	args *a = p;

	produce(a->c, a->v);
}

int main(void)
{
	cell a, b, c, d;
	cell *cs[4] = {&a, &b, &c, &d};

	printf("begin\n");
	for (int i = 0; i < 4; i++) {
		cs[i]->lock = _locknew();
		cs[i]->full = 0;
		cs[i]->taken = 0;
		argblk[i].c = cs[i];
		argblk[i].v = i + 1;
		_cogstart_C(tramp, &argblk[i], stacks[i], sizeof stacks[i]);
		printf("%d\n", consume(cs[i]));
	}
	printf("done\n");
	return 0;
}
