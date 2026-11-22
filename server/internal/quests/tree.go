// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import "slices"

// ChildOrder controls child ordering when a node has no Kids. The exported node tables lost the
// children's order; Kids (the client's behaviour tree files, R-4h) give the real order for every
// tree we ship. The fallbacks only matter for data generated without TreeOrders.json.
type ChildOrder int

const (
	// ChildOrderAsListed keeps table order (quests).
	ChildOrderAsListed ChildOrder = iota
	// ChildOrderTerminalsLast moves a Sequence's QuestSucceed / QuestFailed children last (level plays).
	ChildOrderTerminalsLast
)

// QuestTree is a loaded quest or level play with its tree links (indices into Def.Nodes).
// Ported from QuestTree.cs.
type QuestTree struct {
	Def      QuestDef
	ParentOf []int
	Children [][]int
	Root     int
	index    map[int32]int
}

func (t *QuestTree) Node(index int) NodeDef { return t.Def.Nodes[index] }

// IndexOf returns the index of nodeID in Def.Nodes, or ok=false.
func (t *QuestTree) IndexOf(nodeID int32) (int, bool) {
	i, ok := t.index[nodeID]
	return i, ok
}

// Parent returns the parent's index, or ok=false at the root.
func (t *QuestTree) Parent(index int) (int, bool) {
	if t.ParentOf[index] < 0 {
		return 0, false
	}
	return t.ParentOf[index], true
}

func isTerminalNode(n NodeDef) bool {
	return n.Is("QuestSucceed") || n.Is("QuestFailed")
}

// BuildTree constructs a QuestTree from def, or returns nil when the tree has no nodes or no root.
func BuildTree(def QuestDef, order ChildOrder) *QuestTree {
	n := len(def.Nodes)
	if n == 0 || n > 0xFFFF {
		return nil
	}
	parent := make([]int, n)
	root := -1
	firstByID := make(map[int32]int, n)
	for i := 0; i < n; i++ {
		if _, exists := firstByID[def.Nodes[i].ID]; !exists {
			firstByID[def.Nodes[i].ID] = i
		}
	}
	for i := 0; i < n; i++ {
		parent[i] = -1
		node := def.Nodes[i]
		if node.Parent == 0 {
			if root < 0 {
				root = i
			}
			continue
		}
		if p, ok := firstByID[node.Parent]; ok {
			parent[i] = p
		}
	}
	if root < 0 {
		return nil
	}
	listed := make([][]int, n)
	for j := 0; j < n; j++ {
		if parent[j] >= 0 {
			listed[parent[j]] = append(listed[parent[j]], j)
		}
	}
	children := make([][]int, n)
	for i := 0; i < n; i++ {
		list := listed[i]
		kids := def.Nodes[i].Kids
		if len(kids) > 0 {
			// The real order; children the tree file does not list go last.
			ordered := make([]int, 0, len(list))
			for _, id := range kids {
				for _, c := range list {
					if def.Nodes[c].ID == id {
						ordered = append(ordered, c)
						break
					}
				}
			}
			for _, c := range list {
				if !slices.Contains(kids, def.Nodes[c].ID) {
					ordered = append(ordered, c)
				}
			}
			children[i] = ordered
			continue
		}
		if order == ChildOrderTerminalsLast && def.Nodes[i].Is("Sequence") {
			ordered := make([]int, 0, len(list))
			for _, c := range list {
				if !isTerminalNode(def.Nodes[c]) {
					ordered = append(ordered, c)
				}
			}
			for _, c := range list {
				if isTerminalNode(def.Nodes[c]) {
					ordered = append(ordered, c)
				}
			}
			children[i] = ordered
			continue
		}
		children[i] = list
	}
	return &QuestTree{
		Def:      def,
		ParentOf: parent,
		Children: children,
		Root:     root,
		index:    firstByID,
	}
}
