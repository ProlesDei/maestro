// Package door lists the full argument grammar allowed between the two OS users.
package door

import "slices"

const Path = "/usr/local/libexec/maestro-door"

var verbs = []string{"plan", "rule", "ask", "rules", "stop", "start", "pickup", "ack", "look", "goals", "eta"}

func Cos(verb string) bool    { return slices.Contains(verbs, verb) }
func Wake(args []string) bool { return len(args) == 0 || len(args) == 1 && args[0] == "store-down" }
