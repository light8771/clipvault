//go:build windows && amd64

#include "textflag.h"

TEXT ·PointerFromWin32(SB), NOSPLIT, $0-16
	MOVQ address+0(FP), AX
	MOVQ AX, ret+8(FP)
	RET
