/*
 * A flexcc/flexspin FCACHE bug: a loop polling a Hub cell that another Cog
 * writes stops observing the writes once FCACHE lifts it into Cog RAM.
 *
 * Reduced from OctoGo's channel rendezvous, which hung on hardware for any
 * program using a few channels and a few goroutines. Kept here because the
 * bug is upstream and still live; ogo works around it by building with
 * --fcache=0 (see internal/build/build.go).
 *
 * Build and run on a P2:
 *
 *	flexcc -2 -O1              -o bug.binary flexcc-fcache-bug.c   # hangs
 *	flexcc -2 -O1 --fcache=0   -o ok.binary  flexcc-fcache-bug.c   # works
 *	loadp2 -t -b 230400 bug.binary
 *
 * Expected, and what --fcache=0 prints:
 *
 *	begin
 *	1
 *	2
 *	3
 *	done
 *
 * Actual with FCACHE on: "begin", then nothing. Both sides are alive and
 * spinning -- the producer waits for the consumer to take the value, the
 * consumer never sees full become 1.
 *
 * Two things narrow it down:
 *
 *   - The cell fields are `volatile`, so this is not the compiler being
 *     within its rights to hoist the load out of the loop.
 *
 *   - Where the cells live decides it, which is what points away from the
 *     locks. Same program, only the storage of `cells` changed, FCACHE on:
 *
 *		in main's frame      hangs at once (prints only "begin")
 *		`static` in main     reaches 2 of 3, then hangs
 *		file scope           runs to completion
 *
 *     That a function-scope `static` behaves unlike a file-scope one, for
 *     identical storage, suggests a heuristic rather than a rule.
 *
 * The program's shape decides it too: one cell and one Cog does not
 * reproduce, three does. Tested against flexprop v7.6.11 on a P2-EDGE.
 */

#include <propeller2.h>
#include <stdio.h>

typedef struct {
	int lock;
	volatile int full;  /* set by the producer, polled by the consumer */
	volatile int taken; /* bumped by the consumer, polled by the producer */
	volatile int val;
} cell;

static long stacks[3][256];

/* produce deposits v, then waits for the consumer to take that value. */
static void produce(cell *c, int v)
{
	int mine = 0;

	while (1) {
		if (_locktry(c->lock)) {
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

		if (_locktry(c->lock)) {
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
		if (_locktry(c->lock)) {
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

static args argblk[3];

static void tramp(void *p)
{
	args *a = p;

	produce(a->c, a->v);
}

int main(void)
{
	cell cells[3]; /* in main's frame: moving these to static storage fixes it */

	printf("begin\n");
	for (int i = 0; i < 3; i++) {
		cells[i].lock = _locknew();
		cells[i].full = 0;
		cells[i].taken = 0;
		argblk[i].c = &cells[i];
		argblk[i].v = i + 1;
		_cogstart_C(tramp, &argblk[i], stacks[i], sizeof stacks[i]);
		printf("%d\n", consume(&cells[i]));
	}
	printf("done\n");
	return 0;
}
