module dev.jevido/work/server

go 1.25.0

require (
	dev.jevido/work v0.0.0
	github.com/jackc/pgx/v5 v5.11.0
	golang.org/x/time v0.14.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

// The shared merge lives in the desktop app's module. The dependency runs one
// way on purpose: this module reads dev.jevido/work/internal/ops, and nothing
// in the desktop app imports anything here, so the app never grows a Postgres
// driver just to get at code it already owns.
replace dev.jevido/work => ../
