#include <stdio.h>
#include <stdlib.h>
#include <math.h>
#include <propeller.h>

#define LUT_SIZE 4096
#define SAMPLE_RATE_HZ 100000.0 // 100 kHz update rate

// The pre-calculated sine wave lookup table
uint16_t sine_lut[LUT_SIZE];

int main(int argc, char *argv[]) {
    // 1. Validate arguments
    if (argc != 5) {
        printf("Usage: %s <pin_X> <pin_Y> <freq_X> <freq_Y>\n", argv[0]);
        return 1;
    }

    int pin_x = atoi(argv[1]);
    int pin_y = atoi(argv[2]);
    float freq_x = atof(argv[3]);
    float freq_y = atof(argv[4]);

    printf("OctoGo Hardware Test: Lissajous Curves (DDS)\n");
    printf("X-Axis -> Pin: %d, Freq: %.2f Hz\n", pin_x, freq_x);
    printf("Y-Axis -> Pin: %d, Freq: %.2f Hz\n", pin_y, freq_y);

    // 2. Pre-calculate the Sine Wave Lookup Table (LUT)
    // We map a standard sine wave (-1.0 to 1.0) to the 16-bit DAC range (0 to 65535)
    for(int i = 0; i < LUT_SIZE; i++) {
        float angle = ((float)i / (float)LUT_SIZE) * 2.0 * 3.14159265;
        sine_lut[i] = (uint16_t)((sin(angle) + 1.0) * 32767.5);
    }

    // 3. Configure Smart Pins for both X and Y
    _wrpin(pin_x, P_DAC_124R_3V | P_OE | P_DAC_DITHER_RND);
    _wxpin(pin_x, 1);
    _dirh(pin_x);

    _wrpin(pin_y, P_DAC_124R_3V | P_OE | P_DAC_DITHER_RND);
    _wxpin(pin_y, 1);
    _dirh(pin_y);

    // 4. Set up Direct Digital Synthesis (DDS) variables
    // The phase accumulator is a 32-bit integer. It rolls over automatically,
    // which perfectly mimics the continuous nature of a sine wave.
    uint32_t phase_acc_x = 0;
    uint32_t phase_acc_y = 0;

    // The "tuning word" (step size) determines how fast we move through the LUT.
    // Formula: (Frequency / SampleRate) * 2^32
    uint32_t phase_step_x = (uint32_t)((freq_x / SAMPLE_RATE_HZ) * 4294967296.0);
    uint32_t phase_step_y = (uint32_t)((freq_y / SAMPLE_RATE_HZ) * 4294967296.0);

    // Calculate how many P2 system clock cycles to wait between updates
    unsigned int cycles_per_sample = _clockfreq() / (int)SAMPLE_RATE_HZ;

    printf("Generating... Put your oscilloscope in X-Y mode!\n");

    // 5. The high-speed DDS generation loop
    while(1) {
        // Shift the 32-bit accumulators down to a 12-bit index (0 to 4095)
        uint32_t index_x = phase_acc_x >> 20; 
        uint32_t index_y = phase_acc_y >> 20;

        // Push the voltage to the pins
        _wypin(pin_x, sine_lut[index_x]);
        _wypin(pin_y, sine_lut[index_y]);

        // Advance the phases
        phase_acc_x += phase_step_x;
        phase_acc_y += phase_step_y;

        // Wait for the next sample tick
        _waitx(cycles_per_sample);
    }

    return 0;
}
