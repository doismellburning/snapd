// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2016-2019 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

// Package assertstate implements the manager and state aspects responsible
// for the enforcement of assertions in the system and manages the system-wide
// assertion database.
package assertstate

import (
	"fmt"
	"io"
	"strings"

	"github.com/snapcore/snapd/asserts"
	"github.com/snapcore/snapd/asserts/snapasserts"
	"github.com/snapcore/snapd/httputil"
	"github.com/snapcore/snapd/overlord/snapstate"
	"github.com/snapcore/snapd/overlord/state"
	"github.com/snapcore/snapd/release"
	"github.com/snapcore/snapd/snap"
)

// Add the given assertion to the system assertion database.
func Add(s *state.State, a asserts.Assertion) error {
	// TODO: deal together with asserts itself with (cascading) side effects of possible assertion updates
	return cachedDB(s).Add(a)
}

// Batch allows to accumulate a set of assertions possibly out of prerequisite order and then add them in one go to the system assertion database.
type Batch struct {
	bs         asserts.Backstore
	refs       []*asserts.Ref
	linearized []asserts.Assertion
}

// NewBatch creates a new Batch to accumulate assertions to add in one go to the system assertion database.
func NewBatch() *Batch {
	return &Batch{
		bs:         asserts.NewMemoryBackstore(),
		refs:       nil,
		linearized: nil,
	}
}

func (b *Batch) committing() error {
	if b.linearized != nil {
		return fmt.Errorf("internal error: cannot add to Batch while committing")
	}
	return nil
}

// Add one assertion to the batch.
func (b *Batch) Add(a asserts.Assertion) error {
	if err := b.committing(); err != nil {
		return err
	}

	if !a.SupportedFormat() {
		return &asserts.UnsupportedFormatError{Ref: a.Ref(), Format: a.Format()}
	}
	if err := b.bs.Put(a.Type(), a); err != nil {
		if revErr, ok := err.(*asserts.RevisionError); ok {
			if revErr.Current >= a.Revision() {
				// we already got something more recent
				return nil
			}
		}
		return err
	}
	b.refs = append(b.refs, a.Ref())
	return nil
}

// AddStream adds a stream of assertions to the batch.
// Returns references to to the assertions effectively added.
func (b *Batch) AddStream(r io.Reader) ([]*asserts.Ref, error) {
	if err := b.committing(); err != nil {
		return nil, err
	}

	start := len(b.refs)
	dec := asserts.NewDecoder(r)
	for {
		a, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := b.Add(a); err != nil {
			return nil, err
		}
	}
	added := b.refs[start:]
	if len(added) == 0 {
		return nil, nil
	}
	refs := make([]*asserts.Ref, len(added))
	copy(refs, added)
	return refs, nil
}

func (b *Batch) commitTo(db *asserts.Database) error {
	if err := b.linearize(db); err != nil {
		return err
	}

	// TODO: trigger w. caller a global sanity check if something is revoked
	// (but try to save as much possible still),
	// or err is a check error
	return commitTo(db, b.linearized)
}

func (b *Batch) linearize(db *asserts.Database) error {
	if b.linearized != nil {
		return nil
	}

	retrieve := func(ref *asserts.Ref) (asserts.Assertion, error) {
		a, err := b.bs.Get(ref.Type, ref.PrimaryKey, ref.Type.MaxSupportedFormat())
		if asserts.IsNotFound(err) {
			// fallback to pre-existing assertions
			a, err = ref.Resolve(db.Find)
		}
		if err != nil {
			return nil, findError("cannot find %s", ref, err)
		}
		return a, nil
	}

	// linearize using accumFetcher
	f := newAccumFetcher(db, retrieve)
	for _, ref := range b.refs {
		if err := f.Fetch(ref); err != nil {
			return err
		}
	}

	b.linearized = f.fetched
	return nil
}

// Commit adds the batch of assertions to the system assertion database.
func (b *Batch) Commit(st *state.State) error {
	db := cachedDB(st)

	return b.commitTo(db)
}

// Precheck pre-checks whether adding the batch of assertions to the system assertion database should fully succeed.
func (b *Batch) Precheck(st *state.State) error {
	db := cachedDB(st)
	db = db.WithStackedBackstore(asserts.NewMemoryBackstore())

	return b.commitTo(db)
}

func findError(format string, ref *asserts.Ref, err error) error {
	if asserts.IsNotFound(err) {
		return fmt.Errorf(format, ref)
	} else {
		return fmt.Errorf(format+": %v", ref, err)
	}
}

// RefreshSnapDeclarations refetches all the current snap declarations and their prerequisites.
func RefreshSnapDeclarations(s *state.State, userID int) error {
	deviceCtx, err := snapstate.DevicePastSeeding(s, nil)
	if err != nil {
		return err
	}
	modelAs := deviceCtx.Model()

	snapStates, err := snapstate.All(s)
	if err != nil {
		return nil
	}
	fetching := func(f asserts.Fetcher) error {
		for _, snapst := range snapStates {
			info, err := snapst.CurrentInfo()
			if err != nil {
				return err
			}
			if info.SnapID == "" {
				continue
			}
			if err := snapasserts.FetchSnapDeclaration(f, info.SnapID); err != nil {
				if notRetried, ok := err.(*httputil.PerstistentNetworkError); ok {
					return notRetried
				}
				return fmt.Errorf("cannot refresh snap-declaration for %q: %v", info.InstanceName(), err)
			}
		}

		// fetch store assertion if available
		if modelAs.Store() != "" {
			err := snapasserts.FetchStore(f, modelAs.Store())
			if err != nil && !asserts.IsNotFound(err) {
				return err
			}
		}

		return nil
	}
	return doFetch(s, userID, deviceCtx, fetching)
}

type refreshControlError struct {
	errs []error
}

func (e *refreshControlError) Error() string {
	if len(e.errs) == 1 {
		return e.errs[0].Error()
	}
	l := []string{""}
	for _, e := range e.errs {
		l = append(l, e.Error())
	}
	return fmt.Sprintf("refresh control errors:%s", strings.Join(l, "\n - "))
}

// ValidateRefreshes validates the refresh candidate revisions represented by
// the snapInfos, looking for the needed refresh control validation assertions,
// it returns a validated subset in validated and a summary error if not all
// candidates validated. ignoreValidation is a set of snap-instance-names that
// should not be gated.
func ValidateRefreshes(s *state.State, snapInfos []*snap.Info, ignoreValidation map[string]bool, userID int, deviceCtx snapstate.DeviceContext) (validated []*snap.Info, err error) {
	// maps gated snap-ids to gating snap-ids
	controlled := make(map[string][]string)
	// maps gating snap-ids to their snap names
	gatingNames := make(map[string]string)

	db := DB(s)
	snapStates, err := snapstate.All(s)
	if err != nil {
		return nil, err
	}
	for instanceName, snapst := range snapStates {
		info, err := snapst.CurrentInfo()
		if err != nil {
			return nil, err
		}
		if info.SnapID == "" {
			continue
		}
		gatingID := info.SnapID
		if gatingNames[gatingID] != "" {
			continue
		}
		a, err := db.Find(asserts.SnapDeclarationType, map[string]string{
			"series":  release.Series,
			"snap-id": gatingID,
		})
		if err != nil {
			return nil, fmt.Errorf("internal error: cannot find snap declaration for installed snap %q: %v", instanceName, err)
		}
		decl := a.(*asserts.SnapDeclaration)
		control := decl.RefreshControl()
		if len(control) == 0 {
			continue
		}
		gatingNames[gatingID] = decl.SnapName()
		for _, gatedID := range control {
			controlled[gatedID] = append(controlled[gatedID], gatingID)
		}
	}

	var errs []error
	for _, candInfo := range snapInfos {
		if ignoreValidation[candInfo.InstanceName()] {
			validated = append(validated, candInfo)
			continue
		}
		gatedID := candInfo.SnapID
		gating := controlled[gatedID]
		if len(gating) == 0 { // easy case, no refresh control
			validated = append(validated, candInfo)
			continue
		}

		var validationRefs []*asserts.Ref

		fetching := func(f asserts.Fetcher) error {
			for _, gatingID := range gating {
				valref := &asserts.Ref{
					Type:       asserts.ValidationType,
					PrimaryKey: []string{release.Series, gatingID, gatedID, candInfo.Revision.String()},
				}
				err := f.Fetch(valref)
				if notFound, ok := err.(*asserts.NotFoundError); ok && notFound.Type == asserts.ValidationType {
					return fmt.Errorf("no validation by %q", gatingNames[gatingID])
				}
				if err != nil {
					return fmt.Errorf("cannot find validation by %q: %v", gatingNames[gatingID], err)
				}
				validationRefs = append(validationRefs, valref)
			}
			return nil
		}
		err := doFetch(s, userID, deviceCtx, fetching)
		if err != nil {
			errs = append(errs, fmt.Errorf("cannot refresh %q to revision %s: %v", candInfo.InstanceName(), candInfo.Revision, err))
			continue
		}

		var revoked *asserts.Validation
		for _, valref := range validationRefs {
			a, err := valref.Resolve(db.Find)
			if err != nil {
				return nil, findError("internal error: cannot find just fetched %v", valref, err)
			}
			if val := a.(*asserts.Validation); val.Revoked() {
				revoked = val
				break
			}
		}
		if revoked != nil {
			errs = append(errs, fmt.Errorf("cannot refresh %q to revision %s: validation by %q (id %q) revoked", candInfo.InstanceName(), candInfo.Revision, gatingNames[revoked.SnapID()], revoked.SnapID()))
			continue
		}

		validated = append(validated, candInfo)
	}

	if errs != nil {
		return validated, &refreshControlError{errs}
	}

	return validated, nil
}

// BaseDeclaration returns the base-declaration assertion with policies governing all snaps.
func BaseDeclaration(s *state.State) (*asserts.BaseDeclaration, error) {
	// TODO: switch keeping this in the DB and have it revisioned/updated
	// via the store
	baseDecl := asserts.BuiltinBaseDeclaration()
	if baseDecl == nil {
		return nil, &asserts.NotFoundError{Type: asserts.BaseDeclarationType}
	}
	return baseDecl, nil
}

// SnapDeclaration returns the snap-declaration for the given snap-id if it is present in the system assertion database.
func SnapDeclaration(s *state.State, snapID string) (*asserts.SnapDeclaration, error) {
	db := DB(s)
	a, err := db.Find(asserts.SnapDeclarationType, map[string]string{
		"series":  release.Series,
		"snap-id": snapID,
	})
	if err != nil {
		return nil, err
	}
	return a.(*asserts.SnapDeclaration), nil
}

// Publisher returns the account assertion for publisher of the given snap-id if it is present in the system assertion database.
func Publisher(s *state.State, snapID string) (*asserts.Account, error) {
	db := DB(s)
	a, err := db.Find(asserts.SnapDeclarationType, map[string]string{
		"series":  release.Series,
		"snap-id": snapID,
	})
	if err != nil {
		return nil, err
	}
	snapDecl := a.(*asserts.SnapDeclaration)
	a, err = db.Find(asserts.AccountType, map[string]string{
		"account-id": snapDecl.PublisherID(),
	})
	if err != nil {
		return nil, fmt.Errorf("internal error: cannot find account assertion for the publisher of snap %q: %v", snapDecl.SnapName(), err)
	}
	return a.(*asserts.Account), nil
}

// Store returns the store assertion with the given name/id if it is
// present in the system assertion database.
func Store(s *state.State, store string) (*asserts.Store, error) {
	db := DB(s)
	a, err := db.Find(asserts.StoreType, map[string]string{
		"store": store,
	})
	if err != nil {
		return nil, err
	}
	return a.(*asserts.Store), nil
}

// AutoAliases returns the explicit automatic aliases alias=>app mapping for the given installed snap.
func AutoAliases(s *state.State, info *snap.Info) (map[string]string, error) {
	if info.SnapID == "" {
		// without declaration
		return nil, nil
	}
	decl, err := SnapDeclaration(s, info.SnapID)
	if err != nil {
		return nil, fmt.Errorf("internal error: cannot find snap-declaration for installed snap %q: %v", info.InstanceName(), err)
	}
	explicitAliases := decl.Aliases()
	if len(explicitAliases) != 0 {
		return explicitAliases, nil
	}
	// XXX: old header fallback, just to keep edge working while we fix the
	// store, to remove before next release!
	oldAutoAliases := decl.AutoAliases()
	if len(oldAutoAliases) == 0 {
		return nil, nil
	}
	res := make(map[string]string, len(oldAutoAliases))
	for _, alias := range oldAutoAliases {
		app := info.LegacyAliases[alias]
		if app == nil {
			// not a known alias anymore or yet, skip
			continue

		}
		res[alias] = app.Name
	}
	return res, nil
}

func delayedCrossMgrInit() {
	// hook validation of refreshes into snapstate logic
	snapstate.ValidateRefreshes = ValidateRefreshes
	// hook auto refresh of assertions into snapstate
	snapstate.AutoRefreshAssertions = AutoRefreshAssertions
	// hook retrieving auto-aliases into snapstate logic
	snapstate.AutoAliases = AutoAliases
}

// AutoRefreshAssertions tries to refresh all assertions
func AutoRefreshAssertions(s *state.State, userID int) error {
	return RefreshSnapDeclarations(s, userID)
}
