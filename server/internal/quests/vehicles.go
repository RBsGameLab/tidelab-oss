// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import "tidelab/protocol"

// Ride is a level vehicle the player is seated in (R-99).
type Ride struct {
	Vehicle int64
	Seat    int32
}

type VehicleStats struct {
	Boarded int
	Left    int
	NotDone int
}

// Vehicles tracks the player riding a level vehicle (VehicleEnter / VehicleExitPlayer, R-99).
// Kept in memory only: a ride ends with the session. Ported from Vehicles.cs.
type Vehicles struct {
	Current   *Ride
	Announced *int64
	Stats     VehicleStats
}

func NewVehicles() *Vehicles { return &Vehicles{} }

// Reset clears the ride on a new connection or scene.
func (v *Vehicles) Reset() {
	v.Current = nil
	v.Announced = nil
}

// ParseVehicleEnter parses a VehicleEnter action that seats the player in a named vehicle.
// Returns ok=false for an NPC passenger or the player's own motorcycle.
func ParseVehicleEnter(a QuestAction) (Ride, bool) {
	if typ, ok := ActionString(a, "EnteringTarget", "Type"); !ok || typ != "Player" {
		return Ride{}, false
	}
	var vehicle int64
	if target, ok := ActionInt(a, "EnteringTarget", "TargetVehicle"); ok {
		vehicle = target
	} else {
		vt, ok1 := ActionString(a, "VehicleTarget", "Type")
		id, ok2 := ActionInt(a, "VehicleTarget", "VehicleId")
		if !ok1 || vt != "Appointed" || !ok2 {
			return Ride{}, false
		}
		vehicle = id
	}
	if vehicle <= 0 {
		return Ride{}, false
	}
	seat, ok := ActionInt(a, "Seat")
	if !ok {
		seat = 0
	}
	if seat < 0 || seat > 16 {
		return Ride{}, false
	}
	return Ride{Vehicle: vehicle, Seat: int32(seat)}, true
}

// VehicleEnterIn returns the first VehicleEnter ride in actions, if any.
func VehicleEnterIn(actions []QuestAction) (Ride, bool) {
	for _, a := range actions {
		if a.Name == "VehicleEnter" {
			if r, ok := ParseVehicleEnter(a); ok {
				return r, true
			}
		}
	}
	return Ride{}, false
}

// VehicleExitType maps VehicleExitPlayer {ExitType: {Type}} to ExitVehicleType.
func VehicleExitType(a QuestAction) protocol.ExitVehicleType {
	typ, _ := ActionString(a, "ExitType", "Type")
	switch typ {
	case "Launch":
		return protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeLaunch
	case "SeatStandUp":
		return protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeSeatStandUp
	default:
		return protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal
	}
}

// Board seats the player. netID is nil if the vehicle is not spawned yet (the boarding goes out when it spawns).
func (v *Vehicles) Board(e Ride, playerID int32, netID *int64) *protocol.VehicleUpdateNotify {
	r := e
	v.Current = &r
	v.Stats.Boarded++
	if netID != nil {
		id := *netID
		v.Announced = &id
		return &protocol.VehicleUpdateNotify{PlayerId: playerID, EntityId: id, SeatId: e.Seat}
	}
	v.Announced = nil
	return nil
}

// OnSpawned returns the boarding notify when the ridden vehicle appears.
func (v *Vehicles) OnSpawned(configID, netID int64, playerID int32) *protocol.VehicleUpdateNotify {
	if v.Current == nil || v.Current.Vehicle != configID {
		return nil
	}
	if v.Announced != nil {
		// It went out of range and came back: that ride is over. Seating the player again
		// would pull them into a vehicle they walked away from long ago.
		if *v.Announced != netID {
			v.Reset()
		}
		return nil
	}
	id := netID
	v.Announced = &id
	return &protocol.VehicleUpdateNotify{PlayerId: playerID, EntityId: netID, SeatId: v.Current.Seat}
}

// LeftByClient records that the client reported getting off by themselves.
func (v *Vehicles) LeftByClient(netID int64) bool {
	if !v.IsRidden(netID) {
		return false
	}
	v.Reset()
	v.Stats.Left++
	return true
}

// Leave ends the ride and returns the exit notify (nil if the client was never told about the boarding).
func (v *Vehicles) Leave(playerID int32, exit protocol.ExitVehicleType) *protocol.VehicleUpdateNotify {
	if v.Current == nil {
		return nil
	}
	v.Current = nil
	v.Stats.Left++
	if v.Announced == nil {
		return nil
	}
	id := *v.Announced
	v.Announced = nil
	return &protocol.VehicleUpdateNotify{PlayerId: playerID, EntityId: id, SeatId: -1, ExitType: exit}
}

func (v *Vehicles) IsRidden(netID int64) bool {
	return v.Current != nil && v.Announced != nil && *v.Announced == netID
}

func (v *Vehicles) RidesConfig(configID int64) bool {
	return v.Current != nil && v.Current.Vehicle == configID
}
