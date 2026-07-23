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
static inline void _waitus(uint32_t us) { (void)us; }
static inline void _waitx(int cycles) { (void)cycles; struct timespec t = {0, 1000}; nanosleep(&t, 0); }

/* Pin and misc intrinsics. Off-target there is no hardware, so the pin ops are
   no-ops and the read / counter / random ones return host stand-ins; only _rev is
   a faithful pure function, which OctoGo tests deterministically. */
static inline void _pinh(int pin) { (void)pin; }
static inline void _pinl(int pin) { (void)pin; }
static inline void _pinnot(int pin) { (void)pin; }
static inline void _pinf(int pin) { (void)pin; }
static inline int _pinr(int pin) { (void)pin; return 0; }
static inline void _pinw(int pin, int val) { (void)pin; (void)val; }
static inline void _akpin(int pin) { (void)pin; }
static inline uint32_t _rdpin(int pin) { (void)pin; return 0; }
static inline void _wrpin(int pin, uint32_t val) { (void)pin; (void)val; }
static inline void _wxpin(int pin, uint32_t val) { (void)pin; (void)val; }
static inline void _wypin(int pin, uint32_t val) { (void)pin; (void)val; }
static inline uint32_t _cnt(void) { return (uint32_t)clock(); }
static inline uint32_t _getms(void) { return (uint32_t)((uint64_t)clock() * 1000 / CLOCKS_PER_SEC); }
static inline uint32_t _getsec(void) { return (uint32_t)(clock() / CLOCKS_PER_SEC); }
static inline uint32_t _rnd(void) { return (uint32_t)rand(); }
static inline uint32_t _rev(uint32_t v) {
	uint32_t r = 0;
	for (int i = 0; i < 32; i++) { r = (r << 1) | (v & 1); v >>= 1; }
	return r;
}
static inline void _reboot(void) { }

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

/* Cog ids are recycled once the thread standing in for the cog has run out, so
   that _cogchk models the hardware the emitted pool relies on: a cog id reads as
   running until it genuinely is not. Index 0 is main and is always live. */
#define OGO_HOST_COGS 8
static volatile int ogo_host_cog_live[OGO_HOST_COGS] = {1};
struct ogo_host_start { void (*fn)(void *); void *arg; int cog; };
static void *ogo_host_trampoline(void *p) {
	struct ogo_host_start s = *(struct ogo_host_start *)p;
	free(p);
	s.fn(s.arg);
	ogo_host_cog_live[s.cog] = 0;
	return 0;
}
static inline int _cogstart(void (*fn)(void *), void *arg, void *stack, uint32_t size) {
	(void)stack; (void)size;
	int cog = -1;
	for (int i = 1; i < OGO_HOST_COGS; i++) {
		if (!ogo_host_cog_live[i]) { cog = i; break; }
	}
	if (cog < 0) return -1;
	ogo_host_cog_live[cog] = 1;
	struct ogo_host_start *s = malloc(sizeof *s);
	s->fn = fn; s->arg = arg; s->cog = cog;
	pthread_t t;
	if (pthread_create(&t, 0, ogo_host_trampoline, s) != 0) {
		ogo_host_cog_live[cog] = 0;
		free(s);
		return -1;
	}
	pthread_detach(t);
	return cog;
}
static inline int _cogchk(int cog) {
	return cog >= 0 && cog < OGO_HOST_COGS && ogo_host_cog_live[cog];
}
#define _cogstart_C(f, a, s, n) _cogstart(f, a, s, n)
#endif
