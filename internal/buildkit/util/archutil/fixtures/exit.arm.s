	.global _start
	.text
_start:
	mov %r0, $0x0
	mov %r7, $0x01
	swi 0x0
