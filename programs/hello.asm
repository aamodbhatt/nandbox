; hello.asm: scroll "HELLO NANDBOX" across the LED matrix in rainbow rows.
; Each frame redraws all 16 screen columns from a column-bitmap font.
        .equ ROW0,  4           ; screen row of the top of the text
        .equ DELAY, 60          ; spin iterations between frames

        .data
off:    .byte 0                 ; scroll offset into the text
col:    .byte 0                 ; screen column being drawn
text:   ; 5x7 font, one byte per column, bit 0 = top row, blank column after each letter
        .byte 0x7F,0x08,0x08,0x08,0x7F,0    ; H
        .byte 0x7F,0x49,0x49,0x49,0x41,0    ; E
        .byte 0x7F,0x40,0x40,0x40,0x40,0    ; L
        .byte 0x7F,0x40,0x40,0x40,0x40,0    ; L
        .byte 0x3E,0x41,0x41,0x41,0x3E,0    ; O
        .byte 0,0,0                         ; space
        .byte 0x7F,0x04,0x08,0x10,0x7F,0    ; N
        .byte 0x7E,0x11,0x11,0x11,0x7E,0    ; A
        .byte 0x7F,0x04,0x08,0x10,0x7F,0    ; N
        .byte 0x7F,0x41,0x41,0x22,0x1C,0    ; D
        .byte 0x7F,0x49,0x49,0x49,0x36,0    ; B
        .byte 0x3E,0x41,0x41,0x41,0x3E,0    ; O
        .byte 0x63,0x14,0x08,0x14,0x63,0    ; X
        .byte 0,0,0,0,0                     ; gap before the text repeats
textend:
        .equ LEN, textend - text

        .code
frame:  mov r0, 0
        st  [col], r0
column: ld  r1, [off]
        ld  r0, [col]
        add r1, r0              ; text column = off + col, wrapped into the text
        cmp r1, LEN
        jlo inside
        sub r1, LEN
inside: add r1, text
        ld  r1, [r1]            ; column bitmap
        ld  r2, [col]
        add r2, ROW0*16         ; pixel address of the top row
        mov r3, 7               ; rows left
row:    mov r0, 0
        shr r1                  ; C = next bit
        jnc dark
        mov r0, r3
        add r0, 8               ; colour by row: a rainbow
dark:   vst [r2], r0
        add r2, 16
        sub r3, 1
        jnz row
        ld  r0, [col]
        add r0, 1
        st  [col], r0
        cmp r0, 16
        jnz column
        ld  r0, [off]           ; scroll one column
        add r0, 1
        cmp r0, LEN
        jnz keep
        mov r0, 0
keep:   st  [off], r0
        mov r0, DELAY
wait:   sub r0, 1
        jnz wait
        jmp frame
