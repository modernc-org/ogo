#ifndef OGO_HOST_P2_SHIM_H
#define OGO_HOST_P2_SHIM_H
/* Host shim for the P2 intrinsics the emitted C uses, so OctoGo output can be
   compiled and run off-target. Cogs are pthreads and hardware locks are mutexes;
   the P2 has 8 cogs and 16 locks, and those limits are enforced here too so the
   exhaustion paths are exercisable. */
#include <pthread.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>
#include <time.h>

static inline void _waitms(int ms) { (void)ms; }
static inline void _waitx(int cycles) { (void)cycles; struct timespec t = {0, 1000}; nanosleep(&t, 0); }

#define OGO_HOST_LOCKS 16
static pthread_mutex_t ogo_host_lock[OGO_HOST_LOCKS];
static int ogo_host_lock_used[OGO_HOST_LOCKS];
static pthread_mutex_t ogo_host_lockalloc = PTHREAD_MUTEX_INITIALIZER;

static inline int _locknew(void) {
	pthread_mutex_lock(&ogo_host_lockalloc);
	for (int i = 0; i < OGO_HOST_LOCKS; i++) {
		if (!ogo_host_lock_used[i]) {
			ogo_host_lock_used[i] = 1;
			pthread_mutex_init(&ogo_host_lock[i], 0);
			pthread_mutex_unlock(&ogo_host_lockalloc);
			return i;
		}
	}
	pthread_mutex_unlock(&ogo_host_lockalloc);
	return -1;
}
static inline void _lockret(int l) { if (l >= 0) ogo_host_lock_used[l] = 0; }
/* _locktry returns non-zero when the lock was taken, matching propeller2.h. */
static inline int _locktry(int l) { return pthread_mutex_trylock(&ogo_host_lock[l]) == 0; }
static inline int _lockrel(int l) { return pthread_mutex_unlock(&ogo_host_lock[l]) == 0; }

#define OGO_HOST_COGS 8
static int ogo_host_cogs = 1; /* main occupies the first cog */
struct ogo_host_start { void (*fn)(void *); void *arg; };
static void *ogo_host_trampoline(void *p) {
	struct ogo_host_start s = *(struct ogo_host_start *)p;
	free(p);
	s.fn(s.arg);
	return 0;
}
static inline int _cogstart(void (*fn)(void *), void *arg, void *stack, uint32_t size) {
	(void)stack; (void)size;
	if (ogo_host_cogs >= OGO_HOST_COGS) return -1;
	struct ogo_host_start *s = malloc(sizeof *s);
	s->fn = fn; s->arg = arg;
	pthread_t t;
	if (pthread_create(&t, 0, ogo_host_trampoline, s) != 0) return -1;
	pthread_detach(t);
	return ogo_host_cogs++;
}
#define _cogstart_C(f, a, s, n) _cogstart(f, a, s, n)
#endif
