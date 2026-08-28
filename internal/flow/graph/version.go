package graph

import (
	"errors"
	"time"
)

// ErrPublished is returned by any attempt to mutate a published Version.
//
// I3: a published flow version is immutable. Editing forks a draft. The
// database trigger in migrations/0001 enforces this at the storage layer; this
// error is the same rule enforced in memory, so a bug can't slip through by
// mutating a Version object before it is ever persisted.
var ErrPublished = errors.New("flow version is published and immutable (invariant I3); edit forks a draft")

// Version is one immutable-once-published revision of a Flow.
type Version struct {
	FlowID  string
	Version int
	Graph   Graph

	ContentHash string

	PublishedAt *time.Time
	PublishedBy string
	Changelog   string
}

func (v *Version) IsPublished() bool { return v.PublishedAt != nil }

// SetGraph replaces the graph on a draft version. It refuses on a published
// one — callers must fork instead.
func (v *Version) SetGraph(g Graph) error {
	if v.IsPublished() {
		return ErrPublished
	}
	v.Graph = g
	v.ContentHash = ""
	return nil
}

// Fork produces a new draft Version carrying this version's graph forward, per
// D28. The draft is unpublished and has no version number until publish
// assigns the next one.
func (v *Version) Fork() Version {
	return Version{
		FlowID: v.FlowID,
		Graph:  v.Graph,
	}
}

// Publish freezes the draft: computes the content hash and stamps PublishedAt.
// It refuses to re-publish an already-published version.
func (v *Version) Publish(nextVersion int, by string, at time.Time) error {
	if v.IsPublished() {
		return ErrPublished
	}
	hash, err := v.Graph.ContentHash()
	if err != nil {
		return err
	}
	v.Version = nextVersion
	v.ContentHash = hash
	v.PublishedBy = by
	v.PublishedAt = &at
	return nil
}
