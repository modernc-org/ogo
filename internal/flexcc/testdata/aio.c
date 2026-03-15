#include <stdio.h>
#include <stdlib.h>
#include <propeller.h>

int main(int argc, char *argv[]) {
    // 1. Validate the command line arguments
    if (argc != 3) {
        printf("Usage: %s <pin_number> <voltage (0.0 to 3.3)>\n", argv[0]);
        return 1;
    }

    // 2. Parse the arguments
    int pin = atoi(argv[1]);
    float target_voltage = atof(argv[2]);

    // 3. Clamp the voltage to the valid 0.0V - 3.3V range
    if (target_voltage < 0.0) target_voltage = 0.0;
    if (target_voltage > 3.3) target_voltage = 3.3;

    // 4. The Smart Pin dither mode uses a 16-bit value (0 to 65535)
    // 3.3V / 65535 = ~50 microvolts per step!
    int dac_val = (int)((target_voltage / 3.3) * 65535.0);

    printf("OctoGo Hardware Test: Setting pin %d to %.2fV (16-bit DAC value: %d/65535)\n", pin, target_voltage, dac_val);

    // 5. Configure the pin for DAC output using Smart Pins
    // P_DAC_124R_3V: Configures the physical pin for 124-ohm 3.3V DAC mode.
    // P_OE: Enables the output driver.
    // P_DAC_DITHER_RND: Engages the Smart Pin's hardware PRNG to dither the 
    // internal 8-bit DAC to a steady 16-bit equivalent voltage.
    _wrpin(pin, P_DAC_124R_3V | P_OE | P_DAC_DITHER_RND);

    // 6. Set the Smart Pin configuration parameters
    _wxpin(pin, 1);           // Dither cycle timing (1 clock)
    _wypin(pin, dac_val);     // The target 16-bit analog level

    // 7. Activate the Smart Pin (equivalent to setting DIR high)
    _dirh(pin);

    printf("Success: Pin %d is outputting an analog voltage.\n", pin);

    // 8. Suspend Cog to maintain hardware state for multimeter measurement
    printf("Ready for multimeter measurement. Cog suspended in while(1) loop...\n");
    while(1) {
        _waitx(100000); 
    }

    return 0;
}
