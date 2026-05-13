package main

const maxEditHistory = 5

// editSnapshot captures both panes for undo/redo of merge-style edits (take line, delete line).
type editSnapshot struct {
	leftT, rightT         string
	leftDirty, rightDirty bool
}

func (v *diffView) snapshot() editSnapshot {
	return editSnapshot{
		leftT: v.leftT, rightT: v.rightT,
		leftDirty: v.leftDirty, rightDirty: v.rightDirty,
	}
}

func (v *diffView) clearEditHistory() {
	v.undoStack = v.undoStack[:0]
	v.redoStack = v.redoStack[:0]
	v.refreshUndoRedoButtons()
}

// beginEdit records the current document state before a successful merge mutation.
func (v *diffView) beginEdit() {
	v.undoStack = append(v.undoStack, v.snapshot())
	if len(v.undoStack) > maxEditHistory {
		v.undoStack = v.undoStack[len(v.undoStack)-maxEditHistory:]
	}
	v.redoStack = v.redoStack[:0]
	v.refreshUndoRedoButtons()
}

func (v *diffView) restoreSnapshot(s editSnapshot) {
	v.leftT, v.rightT = s.leftT, s.rightT
	v.leftDirty, v.rightDirty = s.leftDirty, s.rightDirty
	v.recompute()
	v.syncScrollAfterEdit()
	v.refreshMainToolbar()
	v.refreshMainMenu()
	v.refreshUndoRedoButtons()
}

func (v *diffView) undoEdit() {
	if len(v.undoStack) == 0 {
		return
	}
	cur := v.snapshot()
	prev := v.undoStack[len(v.undoStack)-1]
	v.undoStack = v.undoStack[:len(v.undoStack)-1]
	v.redoStack = append(v.redoStack, cur)
	if len(v.redoStack) > maxEditHistory {
		v.redoStack = v.redoStack[len(v.redoStack)-maxEditHistory:]
	}
	v.restoreSnapshot(prev)
}

func (v *diffView) redoEdit() {
	if len(v.redoStack) == 0 {
		return
	}
	cur := v.snapshot()
	next := v.redoStack[len(v.redoStack)-1]
	v.redoStack = v.redoStack[:len(v.redoStack)-1]
	v.undoStack = append(v.undoStack, cur)
	if len(v.undoStack) > maxEditHistory {
		v.undoStack = v.undoStack[len(v.undoStack)-maxEditHistory:]
	}
	v.restoreSnapshot(next)
}
