package agents

// The office is a fixed logical world (see frontend office/world.ts), so desks
// are assigned here in world units rather than being carried in configuration.
// A folder on disk says who an agent is; it does not get to say where they sit.
const (
	worldCentreX = 500.0

	// coordinatorY is the front of the classroom, where the coordinator sits
	// facing the specialists.
	coordinatorY = 140.0
	// seatOffsetY puts an agent in front of their desk rather than on it.
	seatOffsetY = 68.0

	// The specialist rows live between these two y values. The lower bound
	// keeps the last seat inside the walkable floor.
	firstRowY = 300.0
	lastRowY  = 560.0
	// rowStepY is the spacing used while the rows still fit; past that they are
	// compressed to stay within firstRowY..lastRowY.
	rowStepY = 140.0

	// rowSpanX is the width a row may occupy, and maxDeskGapX the spacing it
	// will not exceed however few desks are in it. A row of two therefore
	// lands on the original hand-placed positions, and a fuller row closes up
	// rather than walking off the edge of the world.
	rowSpanX    = 720.0
	maxDeskGapX = 360.0
	perRowMax   = 4
)

// AssignDesks places every agent in the classroom, in list order.
//
// It is a pure function of the roster, so the same set of agents always
// produces the same office. That matters more than preserving any particular
// hand-tuned position: agents now come and go as folders appear on disk, and a
// layout derived from the roster cannot drift out of step with it.
func AssignDesks(list []Agent) {
	var specialists []int
	for i := range list {
		if list[i].Role == RoleCoordinator {
			list[i].Desk = deskAt(worldCentreX, coordinatorY)
			continue
		}
		specialists = append(specialists, i)
	}

	rows := (len(specialists) + perRowMax - 1) / perRowMax
	for n, i := range specialists {
		row := n / perRowMax
		// The last row is usually short; centring it on what it actually holds
		// reads better than leaving a gap where the missing desks would be.
		count := len(specialists) - row*perRowMax
		if count > perRowMax {
			count = perRowMax
		}
		list[i].Desk = deskAt(rowX(n%perRowMax, count), rowY(row, rows))
	}
}

// deskAt builds a desk and the seat in front of it.
func deskAt(x, y float64) Desk {
	return Desk{X: x, Y: y, SeatX: x, SeatY: y + seatOffsetY}
}

// rowX spreads count desks symmetrically about the centre of the room.
func rowX(i, count int) float64 {
	gap := maxDeskGapX
	if tight := rowSpanX / float64(count); tight < gap {
		gap = tight
	}
	return worldCentreX + (float64(i)-float64(count-1)/2)*gap
}

// rowY places row r of rows, compressing the spacing once the rows would
// otherwise run past the back of the floor.
func rowY(r, rows int) float64 {
	if rows <= 1 {
		// A single row sits in the middle of the floor rather than at the top.
		return (firstRowY + lastRowY) / 2
	}
	step := rowStepY
	if tight := (lastRowY - firstRowY) / float64(rows-1); tight < step {
		step = tight
	}
	return firstRowY + float64(r)*step
}
