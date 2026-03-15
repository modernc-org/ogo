#include <stdio.h>
#include <stdlib.h>
#include <propeller.h>

int main(int argc, char *argv[]) {
    // 1. Validate arguments
    if (argc != 6) {
        printf("Usage: %s <pin> <freq_Hz> <min_V> <max_V> <samples_per_tooth>\n", argv[0]);
        return 1;
    }

    // 2. Parse arguments
    int pin = atoi(argv[1]);
    float freq = atof(argv[2]);
    float min_v = atof(argv[3]);
    float max_v = atof(argv[4]);
    int samples = atoi(argv[5]);

    // 3. Sanity clamping
    if (min_v < 0.0) min_v = 0.0;
    if (max_v > 3.3) max_v = 3.3;
    if (min_v > max_v) {
        float temp = min_v; min_v = max_v; max_v = temp;
    }
    if (samples < 2) samples = 2; // Need at least 2 steps for a wave!

    // 4. Calculate 16-bit DAC boundaries (0 to 65535)
    int dac_min = (int)((min_v / 3.3) * 65535.0);
    int dac_max = (int)((max_v / 3.3) * 65535.0);
    int dac_step = (dac_max - dac_min) / samples;

    // 5. Calculate timing
    // System clock frequency / desired wave frequency = cycles per full wave
    // Cycles per wave / samples = cycles per sample
    unsigned int cycles_per_sample = _clockfreq() / (freq * samples);

    printf("OctoGo Hardware Test: Sawtooth / Staircase Wave\n");
    printf("Pin: %d, Freq: %.2f Hz, Min: %.2fV, Max: %.2fV, Samples: %d\n", 
           pin, freq, min_v, max_v, samples);
    printf("DAC Min: %d, DAC Max: %d, Step: %d\n", dac_min, dac_max, dac_step);
    printf("Cycles per sample step: %u\n", cycles_per_sample);

    // 6. Configure Smart Pin for DAC dither mode
    _wrpin(pin, P_DAC_124R_3V | P_OE | P_DAC_DITHER_RND);
    _wxpin(pin, 1);
    _dirh(pin);

    printf("Generating wave... Check your oscilloscope!\n");

    // 7. The continuous waveform loop
    while(1) {
        int current_dac = dac_min;
        for(int i = 0; i < samples; i++) {
            _wypin(pin, current_dac);   // Push new voltage to the pin
            current_dac += dac_step;    // Increment for the next step
            _waitx(cycles_per_sample);  // Block until it's time for the next sample
        }
    }

    return 0;
}
