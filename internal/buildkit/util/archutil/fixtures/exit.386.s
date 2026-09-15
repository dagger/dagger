	.global _start
	.text
_start:
	mov $1, %eax
	xor %ebx, %ebx
	int $0x80
