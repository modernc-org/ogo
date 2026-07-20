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
 *	flexcc -2              -o bug.binary flexcc-fcache-bug.c   # hangs
 *	flexcc -2 --fcache=0   -o ok.binary  flexcc-fcache-bug.c   # works
 *	loadp2 -t -b 230400 bug.binary
 *
 * Expected, and what --fcache=0 prints:
 *
 *	begin
 *	1
 *	2
 *	3
 *	4
 *	done
 *
 * Actual with FCACHE on: stops after 2. Both sides are alive and spinning --
 * the producer waits for the consumer to take the value, the consumer never
 * sees full become 1.
 *
 * The cell fields are `volatile`, so this is not the compiler being within its
 * rights to hoist the load out of the loop.
 *
 * Where the cells live decides it. Same program, only the storage of the four
 * cells changed, FCACHE on, measured on a P2-EDGE against flexprop v7.6.11:
 *
 *	four separate locals (as below)		stops after 2
 *	__builtin_alloca			stops after 0
 *	file-scope static			runs to completion
 *
 * That alloca is *worse* than the plain locals is worth stating, because
 * "prefer alloca over a local array" is the natural advice for code that hands
 * a pointer into a stack frame to another Cog, and here it does not help. It
 * is also what makes this reduction the honest one. An earlier version of this
 * file used a single `cell cells[4]` array instead of four separate locals,
 * and that version *is* fixed by alloca -- so it had been reduced past the
 * point where it still stood in for the original. The OctoGo program it came
 * from behaves like the code below: alloca makes it fail sooner, not later.
 *
 * File-scope storage is the one thing that works, and is not available to the
 * language: a channel's cell has the lifetime of the scope that declares it,
 * so making it static would have two concurrent or recursive calls share one
 * cell.
 *
 * The program's shape decides it too: one cell and one Cog does not reproduce.
 */

#include <propeller2.h>
#include <stdio.h>

typedef struct {
	int lock;
	volatile int full;  /* set by the producer, polled by the consumer */
	volatile int taken; /* bumped by the consumer, polled by the producer */
	volatile int val;
} cell;

static long stacks[4][256];

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

static args argblk[4];

static void tramp(void *p)
{
	args *a = p;

	produce(a->c, a->v);
}

int main(void)
{
	/* Four separate locals, each created and consumed in turn: the shape the
	   OctoGo channel rendezvous emits. One array in their place is a weaker
	   reduction -- see the note at the top. */
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
