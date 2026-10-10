package fixture

import (
	"errors"
	"reflect"
)

func validateChange(r Run) error {
	if r.Change == nil {
		return nil
	}
	if _, err := manifestPaths(r.Change.Target); err != nil {
		return err
	}
	wanted := map[string]Entry{}
	for _, e := range r.Change.Target.Entries {
		wanted[e.ID] = e
	}
	for _, e := range r.Manifest.Entries {
		next, ok := wanted[e.ID]
		if !ok || next.Kind != e.Kind || (e.Parent == "" && !reflect.DeepEqual(e, next)) {
			return errors.New("invalid fixture change scope")
		}
	}
	p := r.Change.Pending
	if p == nil {
		return nil
	}
	current := runEntries(r)
	if !reflect.DeepEqual(current[p.Before.ID], p.Before) || p.Before.ID != p.After.ID || !reflect.DeepEqual(wanted[p.After.ID], p.After) || p.Object.LogicalID != p.Before.ID {
		return errors.New("invalid fixture update intent")
	}
	objects := map[string]Object{}
	for _, o := range r.Objects {
		objects[o.LogicalID] = o
	}
	if !reflect.DeepEqual(objects[p.Object.LogicalID], p.Object) || p.Object.Status != "created" || p.ParentID != objects[p.After.Parent].RemoteID {
		return errors.New("invalid fixture update ownership record")
	}
	return nil
}
