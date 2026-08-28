// Package control holds the control-flow nodes.
//
// Spec §6.4: If, Switch, ForEach, Collect, Loop, Delay, Fallback, Call Flow, Note.
//
// ForEach needs continue-on-error, without which one bad element hides every element after it. Loop is the only legal cycle and its iteration cap is mandatory. Call Flow is the most important reuse primitive: "get one property" authored once, called twelve times.
//
// Note is not executable but is first-class and searchable: this is where people document their own protocol reverse-engineering, and that documentation is worth as much as the flow.
package control
