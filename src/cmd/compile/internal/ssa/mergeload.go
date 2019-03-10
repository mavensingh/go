// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

// mergeload merges loads with other values.
// It must take care to preserve value and block ordering
// and to avoid creating a situation in which flags must be spilled;
// layout, schedule, and flagalloc have all run.
func mergeload(f *Func) {
	if f.Config.mergeValue == nil {
		return
	}

	// if f.pass.debug == 0 {
	// 	return
	// }

	// if !f.DebugTest {
	// 	return
	// }

	invert := f.newSparseSet(f.NumValues())
	defer f.retSparseSet(invert)

	f.mergeLoadState = &mergeLoadState{invert: invert}
	defer func() { f.mergeLoadState = nil }()

	for _, b := range f.Blocks {
		// if b.Control != nil {
		// 	f.Config.mergeValue(b.Control)
		// }
		for i, v := range b.Values {
			if f.Config.mergeValue(v) {
				// Fix up memory arg. Eeeeeeeeeeek.
				for ; i >= 0; i-- {
					w := b.Values[i]
					if w.Type.IsMemory() {
						if w == v.MemoryArg() {
							break
						}
						if w.Op != OpVarDef || w.Aux == v.Aux {
							f.Fatalf("load moved through possibly aliased memory states")
						}
						v.SetArg(len(v.Args)-1, w)
						break
					}
				}
			}
		}
	}

	// Invert all uses of flags that we marked as being inverted.
	for _, b := range f.Blocks {
		if b.Control != nil && f.mergeLoadState.needsInvert(b.Control) {
			b.Kind = invertBlockKind[b.Kind]
		}
		for _, v := range b.Values {
			switch {
			case len(v.Args) > 0 && f.mergeLoadState.needsInvert(v.Args[0]):
				v.Op = invertValueArg0[v.Op]
			case len(v.Args) > 1 && f.mergeLoadState.needsInvert(v.Args[1]):
				v.Op = invertValueArg1[v.Op]
			case len(v.Args) > 2 && f.mergeLoadState.needsInvert(v.Args[2]):
				v.Op = invertValueArg2[v.Op]
			}
		}
	}

	// Remove clobbered values.
	for _, b := range f.Blocks {
		vv := b.Values[:0]
		for _, v := range b.Values {
			if v.Op == OpInvalid {
				f.freeValue(v)
				continue
			}
			vv = append(vv, v)
		}
		// Clear tail to allow GC.
		if len(vv) != len(b.Values) {
			tail := b.Values[len(vv):]
			for j := range tail {
				tail[j] = nil
			}
		}
		b.Values = vv
	}
}

type mergeLoadState struct {
	invert *sparseSet
}

func invertFlags(v *Value) bool {
	v.Block.Func.mergeLoadState.invert.add(v.ID)
	return true
}

func (m *mergeLoadState) needsInvert(v *Value) bool {
	if m.invert.contains(v.ID) {
		return true
	}
	if v.Op == OpSelect0 || v.Op == OpSelect1 {
		return m.invert.contains(v.Args[0].ID)
	}
	return false
}

// canMergeLoadLateClobber reports whether the load can be merged into target without
// invalidating the schedule.
// It also checks that the other non-load argument x is something we
// are ok with clobbering.
func canMergeLoadLateClobber(target, load, x *Value) bool {
	// The register containing x is going to get clobbered.
	// Don't merge if we still need the value of x.
	// We don't have liveness information here, but we can
	// approximate x dying with:
	//  1) target is x's only use.
	//  2) target is not in a deeper loop than x.
	if x.Uses != 1 {
		return false
	}
	loopnest := x.Block.Func.loopnest()
	loopnest.calculateDepths()
	if loopnest.depth(target.Block.ID) > loopnest.depth(x.Block.ID) {
		return false
	}
	return canMergeLoadLate(target, load)
}

// canMergeLoadLate reports whether the load can be merged into target without
// invalidating the schedule.
// TODO: does this correctly handle block Control values??
func canMergeLoadLate(target, load *Value) bool {
	if target.Block.ID != load.Block.ID {
		// If the load is in a different block do not merge it.
		return false
	}

	// We can't merge the load into the target if the load
	// has more than one use.
	if load.Uses != 1 {
		return false
	}

	// We need the load's memory arg to still be alive at target.
	// Values have been scheduled, so load must occur before target.
	// We need to check whether any values that occur between load and target
	// have type memory; if so, it is not safe to merge.
	b := target.Block
	var i int
	for ; b.Values[i] != load; i++ {
	}
	for ; b.Values[i] != target; i++ {
		// TODO: if/when we have alias analysis, if we can prove
		// b.Values[i] doesn't clobber load, we can keep going.
		v := b.Values[i]
		if v.Type.IsMemory() {
			if v.Op == OpVarDef && v.Aux != load.Aux {
				continue
			}
			return false
		}
	}
	return true
}

var invertBlockKind = map[BlockKind]BlockKind{
	BlockAMD64LT:  BlockAMD64GT,
	BlockAMD64GT:  BlockAMD64LT,
	BlockAMD64LE:  BlockAMD64GE,
	BlockAMD64GE:  BlockAMD64LE,
	BlockAMD64ULT: BlockAMD64UGT,
	BlockAMD64UGT: BlockAMD64ULT,
	BlockAMD64ULE: BlockAMD64UGE,
	BlockAMD64UGE: BlockAMD64ULE,
	BlockAMD64EQ:  BlockAMD64EQ,
	BlockAMD64NE:  BlockAMD64NE,
}

var invertValueArg0 = map[Op]Op{
	OpAMD64SETL:  OpAMD64SETG,
	OpAMD64SETG:  OpAMD64SETL,
	OpAMD64SETB:  OpAMD64SETA,
	OpAMD64SETA:  OpAMD64SETB,
	OpAMD64SETLE: OpAMD64SETGE,
	OpAMD64SETGE: OpAMD64SETLE,
	OpAMD64SETBE: OpAMD64SETAE,
	OpAMD64SETAE: OpAMD64SETBE,
	OpAMD64SETEQ: OpAMD64SETEQ,
	OpAMD64SETNE: OpAMD64SETNE,
}

var invertValueArg1 = map[Op]Op{
	OpAMD64SETLstore:  OpAMD64SETGstore,
	OpAMD64SETGstore:  OpAMD64SETLstore,
	OpAMD64SETBstore:  OpAMD64SETAstore,
	OpAMD64SETAstore:  OpAMD64SETBstore,
	OpAMD64SETLEstore: OpAMD64SETGEstore,
	OpAMD64SETGEstore: OpAMD64SETLEstore,
	OpAMD64SETBEstore: OpAMD64SETAEstore,
	OpAMD64SETAEstore: OpAMD64SETBEstore,
	OpAMD64SETEQstore: OpAMD64SETEQstore,
	OpAMD64SETNEstore: OpAMD64SETNEstore,
}

var invertValueArg2 = map[Op]Op{
	OpAMD64CMOVQEQ: OpAMD64CMOVQEQ,
	OpAMD64CMOVQNE: OpAMD64CMOVQNE,
	OpAMD64CMOVQLT: OpAMD64CMOVQGT,
	OpAMD64CMOVQGT: OpAMD64CMOVQLT,
	OpAMD64CMOVQLE: OpAMD64CMOVQGE,
	OpAMD64CMOVQGE: OpAMD64CMOVQLE,
	OpAMD64CMOVQHI: OpAMD64CMOVQCS,
	OpAMD64CMOVQCS: OpAMD64CMOVQHI,
	OpAMD64CMOVQCC: OpAMD64CMOVQLS,
	OpAMD64CMOVQLS: OpAMD64CMOVQCC,

	OpAMD64CMOVLEQ: OpAMD64CMOVLEQ,
	OpAMD64CMOVLNE: OpAMD64CMOVLNE,
	OpAMD64CMOVLLT: OpAMD64CMOVLGT,
	OpAMD64CMOVLGT: OpAMD64CMOVLLT,
	OpAMD64CMOVLLE: OpAMD64CMOVLGE,
	OpAMD64CMOVLGE: OpAMD64CMOVLLE,
	OpAMD64CMOVLHI: OpAMD64CMOVLCS,
	OpAMD64CMOVLCS: OpAMD64CMOVLHI,
	OpAMD64CMOVLCC: OpAMD64CMOVLLS,
	OpAMD64CMOVLLS: OpAMD64CMOVLCC,

	OpAMD64CMOVWEQ: OpAMD64CMOVWEQ,
	OpAMD64CMOVWNE: OpAMD64CMOVWNE,
	OpAMD64CMOVWLT: OpAMD64CMOVWGT,
	OpAMD64CMOVWGT: OpAMD64CMOVWLT,
	OpAMD64CMOVWLE: OpAMD64CMOVWGE,
	OpAMD64CMOVWGE: OpAMD64CMOVWLE,
	OpAMD64CMOVWHI: OpAMD64CMOVWCS,
	OpAMD64CMOVWCS: OpAMD64CMOVWHI,
	OpAMD64CMOVWCC: OpAMD64CMOVWLS,
	OpAMD64CMOVWLS: OpAMD64CMOVWCC,
}
