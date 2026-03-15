#include <stdio.h>
#include <stdlib.h>
#include <propeller.h>

int main(int argc, char *argv[]) {
    // 1. Validate the command line arguments
    if (argc != 3) {
        printf("Usage: %s <pin_number> <value (0 or 1)>\n", argv[0]);
        return 1;
    }

    // 2. Parse the arguments
    int pin = atoi(argv[1]);
    int val = atoi(argv[2]);

    printf("OctoGo Hardware Test: Setting pin %d to %d\n", pin, val);

    // 3. Set the pin state using flexprop built-ins
    // As noted in your design docs, flexprop provides zero-overhead macros
    if (val == 0) {
        _pinl(pin); // Sets pin to digital output and drives LOW
        printf("Success: Pin %d is now LOW (0V).\n", pin);
    } else {
        _pinh(pin); // Sets pin to digital output and drives HIGH
        printf("Success: Pin %d is now HIGH (3.3V).\n", pin);
    }

    // 4. Prevent the boot Cog from terminating
    // If main() returns, the P2 might reset or power down the pins. 
    // This infinite loop yields to prevent Hub bus starvation while you measure.
    printf("Ready for multimeter measurement. Cog suspended in while(1) loop...\n");
    while(1) {
        _waitx(100000); 
    }

    return 0;
}
