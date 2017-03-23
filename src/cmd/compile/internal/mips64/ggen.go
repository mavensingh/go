// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mips64

import (
	"cmd/compile/internal/gc"
	"cmd/internal/obj"
	"cmd/internal/obj/mips"
)

func defframe(pp *gc.Progs, fn *gc.Node, sz int64) {
	// fill in argument size, stack size
	pp.Text.To.Type = obj.TYPE_TEXTSIZE

	pp.Text.To.Val = int32(gc.Rnd(fn.Type.ArgWidth(), int64(gc.Widthptr)))
	frame := uint32(gc.Rnd(sz, int64(gc.Widthreg)))
	pp.Text.To.Offset = int64(frame)

	// insert code to zero ambiguously live variables
	// so that the garbage collector only sees initialized values
	// when it looks for pointers.
	p := pp.Text

	hi := int64(0)
	lo := hi

	// iterate through declarations - they are sorted in decreasing xoffset order.
	for _, n := range fn.Func.Dcl {
		if !n.Name.Needzero() {
			continue
		}
		if n.Class != gc.PAUTO {
			gc.Fatalf("needzero class %d", n.Class)
		}
		if n.Type.Size()%int64(gc.Widthptr) != 0 || n.Xoffset%int64(gc.Widthptr) != 0 || n.Type.Size() == 0 {
			gc.Fatalf("var %L has size %d offset %d", n, int(n.Type.Size()), int(n.Xoffset))
		}

		if lo != hi && n.Xoffset+n.Type.Size() >= lo-int64(2*gc.Widthreg) {
			// merge with range we already have
			lo = n.Xoffset

			continue
		}

		// zero old range
		p = zerorange(pp, p, int64(frame), lo, hi)

		// set new range
		hi = n.Xoffset + n.Type.Size()

		lo = n.Xoffset
	}

	// zero final range
	zerorange(pp, p, int64(frame), lo, hi)
}

func zerorange(pp *gc.Progs, p *obj.Prog, frame int64, lo int64, hi int64) *obj.Prog {
	cnt := hi - lo
	if cnt == 0 {
		return p
	}
	if cnt < int64(4*gc.Widthptr) {
		for i := int64(0); i < cnt; i += int64(gc.Widthptr) {
			p = pp.Appendpp(p, mips.AMOVV, obj.TYPE_REG, mips.REGZERO, 0, obj.TYPE_MEM, mips.REGSP, 8+frame+lo+i)
		}
	} else if cnt <= int64(128*gc.Widthptr) {
		p = pp.Appendpp(p, mips.AADDV, obj.TYPE_CONST, 0, 8+frame+lo-8, obj.TYPE_REG, mips.REGRT1, 0)
		p.Reg = mips.REGSP
		p = pp.Appendpp(p, obj.ADUFFZERO, obj.TYPE_NONE, 0, 0, obj.TYPE_MEM, 0, 0)
		p.To.Name = obj.NAME_EXTERN
		p.To.Sym = gc.Duffzero
		p.To.Offset = 8 * (128 - cnt/int64(gc.Widthptr))
	} else {
		//	ADDV	$(8+frame+lo-8), SP, r1
		//	ADDV	$cnt, r1, r2
		// loop:
		//	MOVV	R0, (Widthptr)r1
		//	ADDV	$Widthptr, r1
		//	BNE		r1, r2, loop
		p = pp.Appendpp(p, mips.AADDV, obj.TYPE_CONST, 0, 8+frame+lo-8, obj.TYPE_REG, mips.REGRT1, 0)
		p.Reg = mips.REGSP
		p = pp.Appendpp(p, mips.AADDV, obj.TYPE_CONST, 0, cnt, obj.TYPE_REG, mips.REGRT2, 0)
		p.Reg = mips.REGRT1
		p = pp.Appendpp(p, mips.AMOVV, obj.TYPE_REG, mips.REGZERO, 0, obj.TYPE_MEM, mips.REGRT1, int64(gc.Widthptr))
		p1 := p
		p = pp.Appendpp(p, mips.AADDV, obj.TYPE_CONST, 0, int64(gc.Widthptr), obj.TYPE_REG, mips.REGRT1, 0)
		p = pp.Appendpp(p, mips.ABNE, obj.TYPE_REG, mips.REGRT1, 0, obj.TYPE_BRANCH, 0, 0)
		p.Reg = mips.REGRT2
		gc.Patch(p, p1)
	}

	return p
}

func ginsnop(pp *gc.Progs) {
	p := pp.Prog(mips.ANOR)
	p.From.Type = obj.TYPE_REG
	p.From.Reg = mips.REG_R0
	p.To.Type = obj.TYPE_REG
	p.To.Reg = mips.REG_R0
}
