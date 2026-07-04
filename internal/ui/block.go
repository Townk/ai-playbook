package ui

import "github.com/Townk/ai-playbook/pkg/playbook"

// Block is the canonical playbook block. Its schema and parser live in
// internal/playbook (ADR-0009 step 1); this alias keeps ui's call sites stable
// while the single owner is playbook.ParseBlocks.
type Block = playbook.Block
