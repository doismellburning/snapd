// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2019 Canonical Ltd
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

package gadget_test

import (
	"errors"
	"path/filepath"

	. "gopkg.in/check.v1"

	"github.com/snapcore/snapd/gadget"
	"github.com/snapcore/snapd/logger"
	"github.com/snapcore/snapd/testutil"
)

type updateTestSuite struct{}

var _ = Suite(&updateTestSuite{})

func (u *updateTestSuite) TestResolveVolumeDifferentName(c *C) {
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"old": {},
		},
	}
	noMatchInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"not-old": {},
		},
	}
	oldVol, newVol, err := gadget.ResolveVolume(oldInfo, noMatchInfo)
	c.Assert(err, ErrorMatches, `cannot find entry for volume "old" in updated gadget info`)
	c.Assert(oldVol, IsNil)
	c.Assert(newVol, IsNil)
}

func (u *updateTestSuite) TestResolveVolumeTooMany(c *C) {
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"old":         {},
			"another-one": {},
		},
	}
	noMatchInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"old": {},
		},
	}
	oldVol, newVol, err := gadget.ResolveVolume(oldInfo, noMatchInfo)
	c.Assert(err, ErrorMatches, `cannot update with more than one volume`)
	c.Assert(oldVol, IsNil)
	c.Assert(newVol, IsNil)
}

func (u *updateTestSuite) TestResolveVolumeSimple(c *C) {
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"old": {Bootloader: "u-boot"},
		},
	}
	noMatchInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"old": {Bootloader: "grub"},
		},
	}
	oldVol, newVol, err := gadget.ResolveVolume(oldInfo, noMatchInfo)
	c.Assert(err, IsNil)
	c.Assert(oldVol, DeepEquals, &gadget.Volume{Bootloader: "u-boot"})
	c.Assert(newVol, DeepEquals, &gadget.Volume{Bootloader: "grub"})
}

type canUpdateTestCase struct {
	from gadget.PositionedStructure
	to   gadget.PositionedStructure
	err  string
}

func (u *updateTestSuite) testCanUpdate(c *C, testCases []canUpdateTestCase) {
	for idx, tc := range testCases {
		c.Logf("tc: %v", idx)
		err := gadget.CanUpdateStructure(&tc.from, &tc.to)
		if tc.err == "" {
			c.Check(err, IsNil)
		} else {
			c.Check(err, ErrorMatches, tc.err)
		}
	}
}

func (u *updateTestSuite) TestCanUpdateSize(c *C) {

	cases := []canUpdateTestCase{
		{
			// size change
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1*gadget.SizeMiB + 1*gadget.SizeKiB},
			},
			err: "cannot change structure size from [0-9]+ to [0-9]+",
		}, {
			// size change
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB},
			},
			err: "",
		},
	}

	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateOffsetWrite(c *C) {

	cases := []canUpdateTestCase{
		{
			// offset-write change
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{Offset: 1024},
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{Offset: 2048},
				},
			},
			err: "cannot change structure offset-write from [0-9]+ to [0-9]+",
		}, {
			// offset-write, change in relative-to structure name
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "foo", Offset: 1024},
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "bar", Offset: 1024},
				},
			},
			err: `cannot change structure offset-write from foo\+[0-9]+ to bar\+[0-9]+`,
		}, {
			// offset-write, unspecified in old
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: nil,
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "bar", Offset: 1024},
				},
			},
			err: `cannot change structure offset-write from unspecified to bar\+[0-9]+`,
		}, {
			// offset-write, unspecified in new
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "foo", Offset: 1024},
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: nil,
				},
			},
			err: `cannot change structure offset-write from foo\+[0-9]+ to unspecified`,
		}, {
			// all ok, both nils
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: nil,
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: nil,
				},
			},
			err: ``,
		}, {
			// all ok, both fully specified
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "foo", Offset: 1024},
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{RelativeTo: "foo", Offset: 1024},
				},
			},
			err: ``,
		}, {
			// all ok, both fully specified
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{Offset: 1024},
				},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{
					OffsetWrite: &gadget.RelativeOffset{Offset: 1024},
				},
			},
			err: ``,
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateOffset(c *C) {

	cases := []canUpdateTestCase{
		{
			// explicitly declared start offset change
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: asSizePtr(1024)},
				StartOffset:     1024,
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: asSizePtr(2048)},
				StartOffset:     2048,
			},
			err: "cannot change structure offset from [0-9]+ to [0-9]+",
		}, {
			// explicitly declared start offset in new structure
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: nil},
				StartOffset:     1024,
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: asSizePtr(2048)},
				StartOffset:     2048,
			},
			err: "cannot change structure offset from unspecified to [0-9]+",
		}, {
			// explicitly declared start offset in old structure,
			// missing from new
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: asSizePtr(1024)},
				StartOffset:     1024,
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB, Offset: nil},
				StartOffset:     2048,
			},
			err: "cannot change structure offset from [0-9]+ to unspecified",
		}, {
			// start offset changed due to positioning
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB},
				StartOffset:     1 * gadget.SizeMiB,
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Size: 1 * gadget.SizeMiB},
				StartOffset:     2 * gadget.SizeMiB,
			},
			err: "cannot change structure start offset from [0-9]+ to [0-9]+",
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateRole(c *C) {

	cases := []canUpdateTestCase{
		{
			// new role
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: ""},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: "system-data"},
			},
			err: `cannot change structure role from "" to "system-data"`,
		}, {
			// explicitly set tole
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: "mbr"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: "system-data"},
			},
			err: `cannot change structure role from "mbr" to "system-data"`,
		}, {
			// implicit legacy role to proper explicit role
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "mbr"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "bare", Role: "mbr"},
			},
			err: "",
		}, {
			// but not in the opposite direction
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "bare", Role: "mbr"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "mbr"},
			},
			err: `cannot change structure type from "bare" to "mbr"`,
		}, {
			// start offset changed due to positioning
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: ""},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Role: ""},
			},
			err: "",
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateType(c *C) {

	cases := []canUpdateTestCase{
		{
			// from hybrid type to GUID
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C,00000000-0000-0000-0000-dd00deadbeef"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadbeef"},
			},
			err: `cannot change structure type from "0C,00000000-0000-0000-0000-dd00deadbeef" to "00000000-0000-0000-0000-dd00deadbeef"`,
		}, {
			// from MBR type to GUID (would be stopped at volume update checks)
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadbeef"},
			},
			err: `cannot change structure type from "0C" to "00000000-0000-0000-0000-dd00deadbeef"`,
		}, {
			// from one MBR type to another
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0A"},
			},
			err: `cannot change structure type from "0C" to "0A"`,
		}, {
			// from one MBR type to another
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "bare"},
			},
			err: `cannot change structure type from "0C" to "bare"`,
		}, {
			// from one GUID to another
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadcafe"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadbeef"},
			},
			err: `cannot change structure type from "00000000-0000-0000-0000-dd00deadcafe" to "00000000-0000-0000-0000-dd00deadbeef"`,
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "bare"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "bare"},
			},
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C"},
			},
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadbeef"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "00000000-0000-0000-0000-dd00deadbeef"},
			},
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C,00000000-0000-0000-0000-dd00deadbeef"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C,00000000-0000-0000-0000-dd00deadbeef"},
			},
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateID(c *C) {

	cases := []canUpdateTestCase{
		{
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{ID: "00000000-0000-0000-0000-dd00deadbeef"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{ID: "00000000-0000-0000-0000-dd00deadcafe"},
			},
			err: `cannot change structure ID from "00000000-0000-0000-0000-dd00deadbeef" to "00000000-0000-0000-0000-dd00deadcafe"`,
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateBareOrFilesystem(c *C) {

	cases := []canUpdateTestCase{
		{
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: ""},
			},
			err: `cannot change a filesystem structure to a bare one`,
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: ""},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4"},
			},
			err: `cannot change a bare structure to filesystem one`,
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "vfat"},
			},
			err: `cannot change filesystem from "ext4" to "vfat"`,
		}, {
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4", Label: "writable"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4"},
			},
			err: `cannot change filesystem label from "writable" to ""`,
		}, {
			// from implicit filesystem label to explicit one
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4", Role: "system-data"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4", Role: "system-data", Label: "writable"},
			},
			err: ``,
		}, {
			// all ok
			from: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4", Label: "do-not-touch"},
			},
			to: gadget.PositionedStructure{
				VolumeStructure: &gadget.VolumeStructure{Type: "0C", Filesystem: "ext4", Label: "do-not-touch"},
			},
			err: ``,
		},
	}
	u.testCanUpdate(c, cases)
}

func (u *updateTestSuite) TestCanUpdateVolume(c *C) {

	for idx, tc := range []struct {
		from gadget.PositionedVolume
		to   gadget.PositionedVolume
		err  string
	}{
		{
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: ""},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "mbr"},
			},
			err: `cannot change volume schema from "gpt" to "mbr"`,
		}, {
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "gpt"},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "mbr"},
			},
			err: `cannot change volume schema from "gpt" to "mbr"`,
		}, {
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{ID: "00000000-0000-0000-0000-0000deadbeef"},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{ID: "00000000-0000-0000-0000-0000deadcafe"},
			},
			err: `cannot change volume ID from "00000000-0000-0000-0000-0000deadbeef" to "00000000-0000-0000-0000-0000deadcafe"`,
		}, {
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{},
				PositionedStructure: []gadget.PositionedStructure{
					{}, {},
				},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{},
				PositionedStructure: []gadget.PositionedStructure{
					{},
				},
			},
			err: `cannot change the number of structures within volume from 2 to 1`,
		}, {
			// valid, implicit schema
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: ""},
				PositionedStructure: []gadget.PositionedStructure{
					{}, {},
				},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "gpt"},
				PositionedStructure: []gadget.PositionedStructure{
					{}, {},
				},
			},
			err: ``,
		}, {
			// valid
			from: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "mbr"},
				PositionedStructure: []gadget.PositionedStructure{
					{}, {},
				},
			},
			to: gadget.PositionedVolume{
				Volume: &gadget.Volume{Schema: "mbr"},
				PositionedStructure: []gadget.PositionedStructure{
					{}, {},
				},
			},
			err: ``,
		},
	} {
		c.Logf("tc: %v", idx)
		err := gadget.CanUpdateVolume(&tc.from, &tc.to)
		if tc.err != "" {
			c.Check(err, ErrorMatches, tc.err)
		} else {
			c.Check(err, IsNil)
		}

	}
}

type mockUpdater struct {
	updateCb   func() error
	backupCb   func() error
	rollbackCb func() error
}

func callOrNil(f func() error) error {
	if f != nil {
		return f()
	}
	return nil
}

func (m *mockUpdater) Backup() error {
	return callOrNil(m.backupCb)
}

func (m *mockUpdater) Rollback() error {
	return callOrNil(m.rollbackCb)
}

func (m *mockUpdater) Update() error {
	return callOrNil(m.updateCb)
}

func updateDataSet(c *C) (oldData gadget.GadgetData, newData gadget.GadgetData, rollbackDir string) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "first",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
	}
	fsStruct := gadget.VolumeStructure{
		Name:       "second",
		Size:       10 * gadget.SizeMiB,
		Filesystem: "ext4",
		Content: []gadget.VolumeContent{
			{Source: "/second-content", Target: "/"},
		},
	}
	lastStruct := gadget.VolumeStructure{
		Name:       "third",
		Size:       5 * gadget.SizeMiB,
		Filesystem: "vfat",
		Content: []gadget.VolumeContent{
			{Source: "/third-content", Target: "/"},
		},
	}
	// start with identical data for new and old infos, they get updated by
	// the caller as needed
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct, fsStruct, lastStruct},
			},
		},
	}
	newInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct, fsStruct, lastStruct},
			},
		},
	}

	oldRootDir := c.MkDir()
	makeSizedFile(c, filepath.Join(oldRootDir, "first.img"), gadget.SizeMiB, nil)
	makeSizedFile(c, filepath.Join(oldRootDir, "/second-content/foo"), 0, nil)
	makeSizedFile(c, filepath.Join(oldRootDir, "/third-content/bar"), 0, nil)
	oldData = gadget.GadgetData{Info: oldInfo, RootDir: oldRootDir}

	newRootDir := c.MkDir()
	makeSizedFile(c, filepath.Join(newRootDir, "first.img"), 900*gadget.SizeKiB, nil)
	makeSizedFile(c, filepath.Join(newRootDir, "/second-content/foo"), gadget.SizeKiB, nil)
	makeSizedFile(c, filepath.Join(newRootDir, "/third-content/bar"), gadget.SizeKiB, nil)
	newData = gadget.GadgetData{Info: newInfo, RootDir: newRootDir}

	rollbackDir = c.MkDir()
	return oldData, newData, rollbackDir
}

func (u *updateTestSuite) TestUpdateApplyHappy(c *C) {
	oldData, newData, rollbackDir := updateDataSet(c)
	// update two structs
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 1

	updaterForStructureCalls := 0
	updateCalls := make(map[string]bool)
	backupCalls := make(map[string]bool)
	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		c.Assert(psRootDir, Equals, newData.RootDir)
		c.Assert(psRollbackDir, Equals, rollbackDir)

		switch updaterForStructureCalls {
		case 0:
			c.Check(ps.Name, Equals, "first")
			c.Check(ps.IsBare(), Equals, true)
			c.Check(ps.Size, Equals, 5*gadget.SizeMiB)
			// non MBR start offset defaults to 1MiB
			c.Check(ps.StartOffset, Equals, 1*gadget.SizeMiB)
			c.Assert(ps.PositionedContent, HasLen, 1)
			c.Check(ps.PositionedContent[0].Image, Equals, "first.img")
			c.Check(ps.PositionedContent[0].Size, Equals, 900*gadget.SizeKiB)
		case 1:
			c.Check(ps.Name, Equals, "second")
			c.Check(ps.IsBare(), Equals, false)
			c.Check(ps.Filesystem, Equals, "ext4")
			c.Check(ps.Size, Equals, 10*gadget.SizeMiB)
			// foo's start offset + foo's size
			c.Check(ps.StartOffset, Equals, (1+5)*gadget.SizeMiB)
			c.Assert(ps.PositionedContent, HasLen, 0)
			c.Assert(ps.Content, HasLen, 1)
			c.Check(ps.Content[0].Source, Equals, "/second-content")
			c.Check(ps.Content[0].Target, Equals, "/")
		default:
			c.Fatalf("unexpected call")
		}
		updaterForStructureCalls++
		mu := &mockUpdater{
			backupCb: func() error {
				backupCalls[ps.Name] = true
				return nil
			},
			updateCb: func() error {
				updateCalls[ps.Name] = true
				return nil
			},
			rollbackCb: func() error {
				c.Fatalf("unexpected call")
				return errors.New("not called")
			},
		}
		return mu, nil
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, IsNil)
	c.Assert(backupCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
	})
	c.Assert(updateCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
	})
	c.Assert(updaterForStructureCalls, Equals, 2)
}

func (u *updateTestSuite) TestUpdateApplyOnlyWhenNeeded(c *C) {
	oldData, newData, rollbackDir := updateDataSet(c)
	// first structure is updated
	oldData.Info.Volumes["foo"].Structure[0].Update.Edition = 0
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	// second one is not, lower edition
	oldData.Info.Volumes["foo"].Structure[1].Update.Edition = 2
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 1
	// third one is not, same edition
	oldData.Info.Volumes["foo"].Structure[2].Update.Edition = 3
	newData.Info.Volumes["foo"].Structure[2].Update.Edition = 3

	updaterForStructureCalls := 0
	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		c.Assert(psRootDir, Equals, newData.RootDir)
		c.Assert(psRollbackDir, Equals, rollbackDir)

		switch updaterForStructureCalls {
		case 0:
			// only called for the first structure
			c.Assert(ps.Name, Equals, "first")
		default:
			c.Fatalf("unexpected call")
		}
		updaterForStructureCalls++
		mu := &mockUpdater{
			rollbackCb: func() error {
				c.Fatalf("unexpected call")
				return errors.New("not called")
			},
		}
		return mu, nil
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, IsNil)
}

func (u *updateTestSuite) TestUpdateApplyErrorPosition(c *C) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "foo",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
	}
	bareStructUpdate := bareStruct
	bareStructUpdate.Update.Edition = 1
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct},
			},
		},
	}
	newInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStructUpdate},
			},
		},
	}

	newRootDir := c.MkDir()
	newData := gadget.GadgetData{Info: newInfo, RootDir: newRootDir}

	oldRootDir := c.MkDir()
	oldData := gadget.GadgetData{Info: oldInfo, RootDir: oldRootDir}

	rollbackDir := c.MkDir()

	// cannot position the old volume without bare struct data
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot lay out the old volume: cannot position structure #0 \("foo"\): content "first.img": .* no such file or directory`)

	makeSizedFile(c, filepath.Join(oldRootDir, "first.img"), gadget.SizeMiB, nil)

	// cannot position the new volume
	err = gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot lay out the new volume: cannot position structure #0 \("foo"\): content "first.img": .* no such file or directory`)
}

func (u *updateTestSuite) TestUpdateApplyErrorIllegalVolumeUpdate(c *C) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "foo",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
	}
	bareStructUpdate := bareStruct
	bareStructUpdate.Name = "foo update"
	bareStructUpdate.Update.Edition = 1
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct},
			},
		},
	}
	newInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				// more structures than old
				Structure: []gadget.VolumeStructure{bareStruct, bareStructUpdate},
			},
		},
	}

	newRootDir := c.MkDir()
	newData := gadget.GadgetData{Info: newInfo, RootDir: newRootDir}

	oldRootDir := c.MkDir()
	oldData := gadget.GadgetData{Info: oldInfo, RootDir: oldRootDir}

	rollbackDir := c.MkDir()

	makeSizedFile(c, filepath.Join(oldRootDir, "first.img"), gadget.SizeMiB, nil)
	makeSizedFile(c, filepath.Join(newRootDir, "first.img"), 900*gadget.SizeKiB, nil)

	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot apply update to volume: cannot change the number of structures within volume from 1 to 2`)
}

func (u *updateTestSuite) TestUpdateApplyErrorIllegalStructureUpdate(c *C) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "foo",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
	}
	fsStruct := gadget.VolumeStructure{
		Name:       "foo",
		Filesystem: "ext4",
		Size:       5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Source: "/", Target: "/"},
		},
		Update: gadget.VolumeUpdate{Edition: 5},
	}
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct},
			},
		},
	}
	newInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				// more structures than old
				Structure: []gadget.VolumeStructure{fsStruct},
			},
		},
	}

	newRootDir := c.MkDir()
	newData := gadget.GadgetData{Info: newInfo, RootDir: newRootDir}

	oldRootDir := c.MkDir()
	oldData := gadget.GadgetData{Info: oldInfo, RootDir: oldRootDir}

	rollbackDir := c.MkDir()

	makeSizedFile(c, filepath.Join(oldRootDir, "first.img"), gadget.SizeMiB, nil)

	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot update volume structure #0 \("foo"\): cannot change a bare structure to filesystem one`)
}

func (u *updateTestSuite) TestUpdateApplyErrorDifferentVolume(c *C) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "foo",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
	}
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct},
			},
		},
	}
	newInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			// same volume info but using a different name
			"foo-new": oldInfo.Volumes["foo"],
		},
	}

	oldData := gadget.GadgetData{Info: oldInfo, RootDir: c.MkDir()}
	newData := gadget.GadgetData{Info: newInfo, RootDir: c.MkDir()}
	rollbackDir := c.MkDir()

	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		c.Fatalf("unexpected call")
		return &mockUpdater{}, nil
	})
	defer restore()

	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot find entry for volume "foo" in updated gadget info`)
}

func (u *updateTestSuite) TestUpdateApplyUpdatesAreOptIn(c *C) {
	// prepare the stage
	bareStruct := gadget.VolumeStructure{
		Name: "foo",
		Size: 5 * gadget.SizeMiB,
		Content: []gadget.VolumeContent{
			{Image: "first.img"},
		},
		Update: gadget.VolumeUpdate{
			Edition: 5,
		},
	}
	oldInfo := &gadget.Info{
		Volumes: map[string]gadget.Volume{
			"foo": {
				Bootloader: "grub",
				Schema:     gadget.GPT,
				Structure:  []gadget.VolumeStructure{bareStruct},
			},
		},
	}

	oldRootDir := c.MkDir()
	oldData := gadget.GadgetData{Info: oldInfo, RootDir: oldRootDir}
	makeSizedFile(c, filepath.Join(oldRootDir, "first.img"), gadget.SizeMiB, nil)

	newRootDir := c.MkDir()
	// same volume description
	newData := gadget.GadgetData{Info: oldInfo, RootDir: newRootDir}
	// different content, but updates are opt in
	makeSizedFile(c, filepath.Join(newRootDir, "first.img"), 900*gadget.SizeKiB, nil)

	rollbackDir := c.MkDir()

	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		c.Fatalf("unexpected call")
		return &mockUpdater{}, nil
	})
	defer restore()

	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, Equals, gadget.ErrNoUpdate)
}

func (u *updateTestSuite) TestUpdateApplyBackupFails(c *C) {
	oldData, newData, rollbackDir := updateDataSet(c)
	// update both structs
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[2].Update.Edition = 3

	updaterForStructureCalls := 0
	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		updater := &mockUpdater{
			updateCb: func() error {
				c.Fatalf("unexpected update call")
				return errors.New("not called")
			},
			rollbackCb: func() error {
				c.Fatalf("unexpected rollback call")
				return errors.New("not called")
			},
		}
		if updaterForStructureCalls == 1 {
			c.Assert(ps.Name, Equals, "second")
			updater.backupCb = func() error {
				return errors.New("failed")
			}
		}
		updaterForStructureCalls++
		return updater, nil
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot backup volume structure #1 \("second"\): failed`)
}

func (u *updateTestSuite) TestUpdateApplyUpdateFailsThenRollback(c *C) {
	oldData, newData, rollbackDir := updateDataSet(c)
	// update all structs
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 2
	newData.Info.Volumes["foo"].Structure[2].Update.Edition = 3

	updateCalls := make(map[string]bool)
	backupCalls := make(map[string]bool)
	rollbackCalls := make(map[string]bool)
	updaterForStructureCalls := 0
	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		updater := &mockUpdater{
			backupCb: func() error {
				backupCalls[ps.Name] = true
				return nil
			},
			rollbackCb: func() error {
				rollbackCalls[ps.Name] = true
				return nil
			},
			updateCb: func() error {
				updateCalls[ps.Name] = true
				return nil
			},
		}
		if updaterForStructureCalls == 1 {
			c.Assert(ps.Name, Equals, "second")
			// fail update of 2nd structure
			updater.updateCb = func() error {
				updateCalls[ps.Name] = true
				return errors.New("failed")
			}
		}
		updaterForStructureCalls++
		return updater, nil
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot update volume structure #1 \("second"\): failed`)
	c.Assert(backupCalls, DeepEquals, map[string]bool{
		// all were backed up
		"first":  true,
		"second": true,
		"third":  true,
	})
	c.Assert(updateCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
		// third was never updated, as second failed
	})
	c.Assert(rollbackCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
		// third does not need as it was not updated
	})
}

func (u *updateTestSuite) TestUpdateApplyUpdateErrorRollbackFail(c *C) {
	logbuf, restore := logger.MockLogger()
	defer restore()

	oldData, newData, rollbackDir := updateDataSet(c)
	// update all structs
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 2
	newData.Info.Volumes["foo"].Structure[2].Update.Edition = 3

	updateCalls := make(map[string]bool)
	backupCalls := make(map[string]bool)
	rollbackCalls := make(map[string]bool)
	updaterForStructureCalls := 0
	restore = gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		updater := &mockUpdater{
			backupCb: func() error {
				backupCalls[ps.Name] = true
				return nil
			},
			rollbackCb: func() error {
				rollbackCalls[ps.Name] = true
				return nil
			},
			updateCb: func() error {
				updateCalls[ps.Name] = true
				return nil
			},
		}
		switch updaterForStructureCalls {
		case 1:
			c.Assert(ps.Name, Equals, "second")
			// rollback fails on 2nd structure
			updater.rollbackCb = func() error {
				rollbackCalls[ps.Name] = true
				return errors.New("rollback failed with different error")
			}
		case 2:
			c.Assert(ps.Name, Equals, "third")
			// fail update of 3rd structure
			updater.updateCb = func() error {
				updateCalls[ps.Name] = true
				return errors.New("update error")
			}
		}
		updaterForStructureCalls++
		return updater, nil
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	// preserves update error
	c.Assert(err, ErrorMatches, `cannot update volume structure #2 \("third"\): update error`)
	c.Assert(backupCalls, DeepEquals, map[string]bool{
		// all were backed up
		"first":  true,
		"second": true,
		"third":  true,
	})
	c.Assert(updateCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
		"third":  true,
	})
	c.Assert(rollbackCalls, DeepEquals, map[string]bool{
		"first":  true,
		"second": true,
		"third":  true,
	})

	c.Check(logbuf.String(), testutil.Contains, `cannot update gadget: cannot update volume structure #2 ("third"): update error`)
	c.Check(logbuf.String(), testutil.Contains, `cannot rollback volume structure #1 ("second") update: rollback failed with different error`)
}

func (u *updateTestSuite) TestUpdateApplyBadUpdater(c *C) {
	oldData, newData, rollbackDir := updateDataSet(c)
	// update all structs
	newData.Info.Volumes["foo"].Structure[0].Update.Edition = 1
	newData.Info.Volumes["foo"].Structure[1].Update.Edition = 2
	newData.Info.Volumes["foo"].Structure[2].Update.Edition = 3

	restore := gadget.MockUpdaterForStructure(func(ps *gadget.PositionedStructure, psRootDir, psRollbackDir string) (gadget.Updater, error) {
		return nil, errors.New("bad updater for structure")
	})
	defer restore()

	// go go go
	err := gadget.Update(oldData, newData, rollbackDir)
	c.Assert(err, ErrorMatches, `cannot prepare update for volume structure #0 \("first"\): bad updater for structure`)
}

func (u *updateTestSuite) TestUpdaterForStructure(c *C) {
	rootDir := c.MkDir()
	rollbackDir := c.MkDir()

	psBare := &gadget.PositionedStructure{
		VolumeStructure: &gadget.VolumeStructure{
			Filesystem: "none",
			Size:       10 * gadget.SizeMiB,
		},
		StartOffset: 1 * gadget.SizeMiB,
	}
	updater, err := gadget.UpdaterForStructure(psBare, rootDir, rollbackDir)
	c.Assert(err, IsNil)
	c.Assert(updater, FitsTypeOf, &gadget.RawStructureUpdater{})

	psFs := &gadget.PositionedStructure{
		VolumeStructure: &gadget.VolumeStructure{
			Filesystem: "ext4",
			Size:       10 * gadget.SizeMiB,
		},
		StartOffset: 1 * gadget.SizeMiB,
	}
	updater, err = gadget.UpdaterForStructure(psFs, rootDir, rollbackDir)
	c.Assert(err, IsNil)
	c.Assert(updater, FitsTypeOf, &gadget.MountedFilesystemUpdater{})

	// trigger errors
	updater, err = gadget.UpdaterForStructure(psBare, rootDir, "")
	c.Assert(err, ErrorMatches, "internal error: backup directory cannot be unset")
	c.Assert(updater, IsNil)

	updater, err = gadget.UpdaterForStructure(psFs, "", rollbackDir)
	c.Assert(err, ErrorMatches, "internal error: gadget content directory cannot be unset")
	c.Assert(updater, IsNil)
}
