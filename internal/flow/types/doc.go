// Package types defines the Frame type system and the edit-time connection validation rules.
//
// Spec §6.1, decisions D4 and D5, invariant I1.
//
// A refusal must always carry an inline reason and a suggested fix node. That suggestion is what makes typed ports feel like help rather than obstruction; without it they are merely an obstacle.
package types
