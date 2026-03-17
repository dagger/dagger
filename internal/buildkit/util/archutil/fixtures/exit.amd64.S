                     // SSE3     SSSE3    CMPXCHNG16 SSE4.1    SSE4.2    POPCNT
#define V2_FEATURES_CX (1 << 0 | 1 << 9 | 1 << 13  | 1 << 19 | 1 << 20 | 1 << 23)
                         // LAHF/SAHF
#define V2_EXT_FEATURES_CX (1 << 0)

// 64-113 is user-defined exit codes
// https://tldp.org/LDP/abs/html/exitcodes.html
#define exitcode(x) $((x) + 64)

  .global _start
  .text
_start:
  // highest basic calling parameter
  xor %rax, %rax
  cpuid
  cmp $7, %rax
  jl v1

  // highest extended calling parameter
  mov $0x80000000, %rax
  cpuid
  cmp $0x80000001, %eax
  jl v1

  // feature bits
  mov $1, %rax
  xor %rcx, %rcx
  cpuid
  and $V2_FEATURES_CX, %rcx 
  cmp $V2_FEATURES_CX, %rcx
  jne v1

  // extended feature bits
  mov $0x80000001, %rax
  xor %rcx, %rcx
  cpuid
  and $V2_EXT_FEATURES_CX, %rcx
  cmp $V2_EXT_FEATURES_CX, %rcx
  jne v1

  jmp v2
v1:
  mov exitcode(1), %rdi
  jmp exit
v2:
  mov exitcode(2), %rdi
  jmp exit
exit:
  mov $60, %rax
  syscall
