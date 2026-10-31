; counter.asm: count up forever on the LEDs and the score display.
        .equ LEDS,  0xF3
        .equ SCORE, 0xF2
        .equ DELAY, 200     ; spin iterations between counts

start:  mov r0, 0
loop:   st  [LEDS], r0
        st  [SCORE], r0
        mov r1, DELAY
wait:   sub r1, 1
        jnz wait
        add r0, 1
        jmp loop
