package main

import (
	"errors"
	"strings"
)

// Cold recovery may reuse only its original managed dead pane. A live shell,
// unknown tmux observation, human focus, pin or peer manager is not dead-host
// evidence and must never be overridden. The caller holds the root lock and
// separately proves old SDK absence and exclusive original node ownership.
func validateColdNativePlacement(job DelegationJob, view View, pane string, probe func(...string) (string, error)) error {
	if view.ID == "" || view.Kind != "agent" || view.Target != job.ChildID || view.Manager != job.ParentID || view.Pinned {
		return errors.New("cold native view is absent, pinned or owned by another manager")
	}
	if pane == "" {
		return nil
	}
	dead, err := probe("display-message", "-p", "-t", pane, "#{pane_dead}")
	if err != nil || strings.TrimSpace(dead) != "1" {
		return errors.New("cold native pane is live or its death is unproven; no shell replacement")
	}
	clients, err := probe("list-clients", "-F", "#{pane_id}")
	if err != nil {
		return errors.New("cold native human focus is unknown; no pane replacement")
	}
	for _, focused := range strings.Split(clients, "\n") {
		if strings.TrimSpace(focused) == pane {
			return errors.New("cold native pane is actively used by a human")
		}
	}
	return nil
}
