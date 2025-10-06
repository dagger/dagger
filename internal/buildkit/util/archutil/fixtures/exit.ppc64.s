	.global _start
	.section ".opd","aw"
_start:
	.quad .L.start,.TOC.@tocbase,0
	.text
	.abiversion 1
.L.start:
	li %r0, 1
	li %r3, 0
	sc
