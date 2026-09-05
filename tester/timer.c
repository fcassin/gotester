#include "timer.h"
#include <stdio.h>
#include <x86intrin.h>

u64 ReadCPUTimer(void) {
    return __rdtsc();
}
