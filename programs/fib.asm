; fib.asm: store the Fibonacci numbers that fit in a byte at RAM[0..13],
; show how many were stored on the LEDs, then halt.
        .equ LEDS, 0xF3

start:  mov r0, 0           ; a
        mov r1, 1           ; b
        mov r2, 0           ; output pointer
loop:   st  [r2], r0
        add r2, 1
        mov r3, r0
        add r3, r1          ; a + b; carry means it no longer fits
        jc  done
        mov r0, r1
        mov r1, r3
        jmp loop
done:   st  [r2], r1
        add r2, 1
        st  [LEDS], r2
        hlt
