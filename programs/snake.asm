; snake.asm: Snake on the 16x16 LED matrix.
;
; The screen is the game state. A pixel holds 0 (empty), FOOD, HEAD, or the
; direction (1..4) the snake travelled when it left that cell, so the tail can
; follow the body without any list in RAM. Positions are one byte: row*16+col.
;
; Controls: arrows steer (no reversing). After a crash, release every key and
; press any key to play again. The score is kept in BCD on the 7-segment display.

        .equ INPUT, 0xF0
        .equ RAND,  0xF1
        .equ SCORE, 0xF2
        .equ LEDS,  0xF3

        .equ UP, 1              ; directions (also the body pixel values)
        .equ RIGHT, 2
        .equ DOWN, 3
        .equ LEFT, 4
        .equ FOOD, 5
        .equ HEAD, 6
        .equ CRASH, 7

        .equ JOY_UP, 1          ; joystick bits at INPUT
        .equ JOY_DOWN, 2
        .equ JOY_LEFT, 4
        .equ JOY_RIGHT, 8

        .equ DELAY, 72          ; input-polling iterations per move: ~7 moves/s at 60 kHz
        .equ SPIN, 24           ; busy-wait inside each polling iteration

        .data
head:   .byte 0
tail:   .byte 0
dir:    .byte 0
keys:   .byte 0                 ; last joystick state seen while waiting
score:  .byte 0

        .code
reset:  mov r0, 0               ; clear the screen (256 pixels; r0 wraps to 0)
        mov r1, 0
clear:  vst [r0], r1
        add r0, 1
        jnz clear
        st  [score], r0
        st  [SCORE], r0
        st  [keys], r0
        st  [LEDS], r0
        mov r1, RIGHT
        st  [dir], r1
        mov r0, 0x86            ; a three-cell snake on row 8 heading right
        st  [tail], r0
        vst [r0], r1
        add r0, 1
        vst [r0], r1
        add r0, 1
        st  [head], r0
        mov r1, HEAD
        vst [r0], r1
        call food

loop:   mov r2, DELAY           ; wait, remembering the latest key press
wait:   ld  r0, [INPUT]
        cmp r0, 0
        jz  idle
        st  [keys], r0
idle:   mov r1, SPIN
spin:   sub r1, 1
        jnz spin
        sub r2, 1
        jnz wait

        ld  r0, [keys]          ; turn, unless it would reverse into the body
        mov r1, 0
        st  [keys], r1
        ld  r2, [dir]
        mov r1, r2
        tst r0, JOY_UP
        jz  t1
        cmp r2, DOWN
        jz  t1
        mov r1, UP
t1:     tst r0, JOY_DOWN
        jz  t2
        cmp r2, UP
        jz  t2
        mov r1, DOWN
t2:     tst r0, JOY_LEFT
        jz  t3
        cmp r2, RIGHT
        jz  t3
        mov r1, LEFT
t3:     tst r0, JOY_RIGHT
        jz  t4
        cmp r2, LEFT
        jz  t4
        mov r1, RIGHT
t4:     st  [dir], r1

        ld  r0, [head]          ; the old head remembers which way we went
        vst [r0], r1
        call step
        st  [head], r0
        vld r2, [r0]            ; what are we moving onto?
        cmp r2, FOOD
        jz  eat
        cmp r2, 0
        jnz dead
        mov r1, HEAD
        vst [r0], r1

        ld  r0, [tail]          ; follow the tail's arrow and erase it
        vld r1, [r0]
        mov r2, 0
        vst [r0], r2
        call step
        st  [tail], r0
        jmp loop

eat:    mov r1, HEAD            ; growing = not moving the tail this turn
        vst [r0], r1
        ld  r0, [score]         ; BCD increment
        add r0, 1
        mov r1, r0
        and r1, 0x0F
        cmp r1, 10
        jnz scored
        add r0, 6
scored: st  [score], r0
        st  [SCORE], r0
        call food
        jmp loop

dead:   mov r1, CRASH
        vst [r0], r1
        mov r0, 0xFF
        st  [LEDS], r0
release: ld r0, [INPUT]         ; wait for all keys up, then any key down
        cmp r0, 0
        jnz release
press:  ld  r0, [INPUT]
        cmp r0, 0
        jz  press
        jmp reset

; step: move position r0 one cell in direction r1, wrapping at the edges.
; Clobbers r2; returns through r3.
step:   cmp r1, UP
        jnz s1
        sub r0, 16
        ret
s1:     cmp r1, DOWN
        jnz s2
        add r0, 16
        ret
s2:     mov r2, r0
        and r2, 0xF0            ; keep the row, wrap the column
        cmp r1, LEFT
        jnz s3
        sub r0, 1
        jmp s4
s3:     add r0, 1
s4:     and r0, 0x0F
        or  r0, r2
        ret

; food: put FOOD on a random empty cell. Clobbers r0, r1.
food:   ld  r0, [RAND]
        vld r1, [r0]
        cmp r1, 0
        jnz food
        mov r1, FOOD
        vst [r0], r1
        ret
