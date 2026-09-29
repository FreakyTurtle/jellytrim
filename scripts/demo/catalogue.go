package main

// The demo's invented library. Every title, person and series here is made
// up. Edit the tables freely: the demo checks at start that every name the
// history and queue tables use exists, and that the policies chose it.
//
// Sizes are in GB (10^9 bytes). "Days" columns count back from today, so the
// demo always looks recently used.

// The household. jo has not signed in for months, so the default 90-day
// inactive filter leaves jo's viewing out of every decision.
const (
	sam   = "sam"
	alex  = "alex"
	robin = "robin"
	jo    = "jo"
)

type person struct {
	Name       string
	Admin      bool
	ActiveDays int // days since the user last used Jellyfin
}

var household = []person{
	{Name: sam, Admin: true, ActiveDays: 0},
	{Name: alex, ActiveDays: 1},
	{Name: robin, ActiveDays: 6},
	{Name: jo, ActiveDays: 142},
}

// source is the kind of file, which picks the ffprobe fixture the fake
// prober starts from and the audio and subtitle tracks it adds.
type source string

const (
	uhdH264      source = "4K H.264 remux"
	uhdHDR10     source = "4K HEVC HDR10 remux"
	uhdDV5       source = "4K Dolby Vision profile 5"
	fhdH264      source = "1080p H.264"
	fhdScope     source = "1080p H.264 scope"
	fhdHEVC      source = "1080p HEVC"
	fhdAV1       source = "1080p AV1"
	webH264      source = "1080p H.264 web episode"
	webHEVC      source = "1080p HEVC web episode"
	interlaced   source = "1080i broadcast recording"
	optimisedOut source = "JellyTrim output" // only for files a past job replaced
)

// seen maps a user to how many days ago they last watched the item.
type seen map[string]int

type film struct {
	Title   string
	Year    int
	Source  source
	GB      float64
	Minutes int
	Added   int // days since it was added to Jellyfin
	Seen    seen
	Fav     []string // users who marked it a favourite
	Genres  []string
	Tags    []string
	// HardLinked gives the file a second name outside the libraries, as a
	// torrent client's seeding copy would. JellyTrim skips such files.
	HardLinked bool
}

var films = []film{
	// Watched 4K films: Archive watched 4K chooses these once the last viewing is 90 days old.
	{Title: "The Glass Orchard", Year: 2019, Source: uhdH264, GB: 58.4, Minutes: 132, Added: 820, Seen: seen{sam: 410, alex: 402}, Genres: g("Drama")},
	{Title: "Northbound Freight", Year: 2021, Source: uhdHDR10, GB: 64.2, Minutes: 146, Added: 610, Seen: seen{sam: 220}, Genres: g("Thriller")},
	{Title: "Salt and Cinder", Year: 2017, Source: uhdH264, GB: 49.8, Minutes: 118, Added: 700, Seen: seen{alex: 300}, Genres: g("Drama", "History")},
	{Title: "A Map of Small Hours", Year: 2020, Source: uhdHDR10, GB: 52.6, Minutes: 124, Added: 540, Seen: seen{robin: 150, sam: 148}, Genres: g("Romance")},
	{Title: "Halcyon Loop", Year: 2014, Source: uhdH264, GB: 61.0, Minutes: 141, Added: 730, Seen: seen{sam: 700, alex: 700, robin: 690}, Genres: g("Science Fiction")},
	{Title: "Halcyon Loop: Drift", Year: 2016, Source: uhdHDR10, GB: 66.3, Minutes: 152, Added: 730, Seen: seen{sam: 690, alex: 688}, Genres: g("Science Fiction")},
	{Title: "Halcyon Loop: Landfall", Year: 2019, Source: uhdHDR10, GB: 69.5, Minutes: 158, Added: 700, Seen: seen{sam: 680, alex: 679}, Fav: []string{alex}, Genres: g("Science Fiction")},
	{Title: "The Lantern Keeper", Year: 2022, Source: uhdHDR10, GB: 47.9, Minutes: 109, Added: 400, Seen: seen{alex: 130}, Genres: g("Mystery")},
	{Title: "The Copperline Job", Year: 2018, Source: uhdH264, GB: 44.1, Minutes: 101, Added: 650, Seen: seen{sam: 260}, Genres: g("Crime", "Comedy")},
	{Title: "The Cartographer's Daughter", Year: 2012, Source: uhdHDR10, GB: 57.2, Minutes: 142, Added: 900, Seen: seen{robin: 400}, Genres: g("Drama")},
	{Title: "Twelve Pale Lanterns", Year: 2011, Source: uhdH264, GB: 42.3, Minutes: 103, Added: 880, Seen: seen{robin: 175}, Genres: g("Fantasy")},
	// 4K films that are skipped with a reason, or not chosen yet.
	{Title: "Signal Box", Year: 2023, Source: uhdDV5, GB: 21.7, Minutes: 119, Added: 300, Seen: seen{sam: 200}, Genres: g("Thriller")},
	{Title: "Nine Bells for Hollin", Year: 2018, Source: uhdHDR10, GB: 58.8, Minutes: 137, Added: 500, Seen: seen{alex: 310}, Genres: g("War", "Drama"), HardLinked: true},
	{Title: "Iron Meadow", Year: 2023, Source: uhdHDR10, GB: 55.0, Minutes: 127, Added: 90, Seen: seen{sam: 21, alex: 21}, Genres: g("Western")},
	{Title: "Low Orbit Lullaby", Year: 2024, Source: uhdHDR10, GB: 61.7, Minutes: 139, Added: 12, Genres: g("Science Fiction")},
	{Title: "Undertow Hotel", Year: 2018, Source: uhdH264, GB: 53.7, Minutes: 128, Added: 480, Seen: seen{jo: 300}, Genres: g("Horror")},
	// 1080p films. Efficient encoding is off, so no policy chooses these.
	{Title: "Paper Kites at Dusk", Year: 2016, Source: fhdH264, GB: 14.2, Minutes: 117, Added: 760, Seen: seen{alex: 500}, Genres: g("Drama")},
	{Title: "The Weight of Rainwater", Year: 2015, Source: fhdH264, GB: 12.8, Minutes: 116, Added: 640, Seen: seen{robin: 220, sam: 219}, Genres: g("Drama")},
	{Title: "Tidewater Road", Year: 2012, Source: fhdH264, GB: 9.6, Minutes: 98, Added: 990, Seen: seen{sam: 45}, Genres: g("Road Movie")},
	{Title: "Fathom Street", Year: 2013, Source: fhdScope, GB: 11.3, Minutes: 124, Added: 950, Seen: seen{alex: 700}, Genres: g("Crime")},
	{Title: "The Quiet Engineer", Year: 2020, Source: fhdHEVC, GB: 4.2, Minutes: 112, Added: 420, Seen: seen{sam: 80}, Fav: []string{sam}, Genres: g("Documentary"), Tags: g("documentary")},
	{Title: "A Brass Moon over Kettleby", Year: 2011, Source: fhdH264, GB: 17.9, Minutes: 139, Added: 1000, Seen: seen{sam: 600}, Fav: []string{sam}, Genres: g("Comedy")},
	{Title: "Kestrel Point", Year: 2022, Source: fhdHEVC, GB: 5.6, Minutes: 131, Added: 200, Seen: seen{alex: 150}, Genres: g("Adventure")},
	{Title: "Winter at Halden Mill", Year: 2010, Source: fhdH264, GB: 10.4, Minutes: 104, Added: 1010, Seen: seen{robin: 380}, Genres: g("Family"), Tags: g("christmas")},
	{Title: "Loose Threads, Tight Knots", Year: 2018, Source: fhdHEVC, GB: 3.4, Minutes: 94, Added: 330, Genres: g("Comedy")},
	{Title: "The Understudy's Understudy", Year: 2017, Source: fhdH264, GB: 8.7, Minutes: 91, Added: 580, Seen: seen{sam: 95, robin: 94}, Genres: g("Comedy")},
	{Title: "Spinning Top Island", Year: 2021, Source: fhdAV1, GB: 3.1, Minutes: 88, Added: 260, Seen: seen{robin: 10}, Genres: g("Animation", "Family"), Tags: g("kids")},
	{Title: "Otterburn and the Clockwork Fox", Year: 2019, Source: fhdH264, GB: 8.2, Minutes: 92, Added: 450, Seen: seen{robin: 3}, Fav: []string{robin}, Genres: g("Animation", "Family"), Tags: g("kids")},
	{Title: "Starling Parade", Year: 2016, Source: fhdH264, GB: 9.1, Minutes: 97, Added: 700, Seen: seen{robin: 120}, Genres: g("Animation", "Family"), Tags: g("kids")},
	{Title: "The Last Ferry to Marrow Isle", Year: 2014, Source: fhdH264, GB: 15.6, Minutes: 127, Added: 860, Seen: seen{alex: 330}, Genres: g("Mystery")},
	{Title: "Cold Frame Gardens", Year: 2009, Source: fhdH264, GB: 13.3, Minutes: 108, Added: 1020, Genres: g("Drama")},
	{Title: "Harrow & Vane", Year: 2015, Source: fhdH264, GB: 16.4, Minutes: 121, Added: 610, Seen: seen{sam: 140}, Genres: g("Crime")},
	{Title: "A Field Guide to Leaving", Year: 2021, Source: fhdHEVC, GB: 4.9, Minutes: 118, Added: 280, Seen: seen{alex: 60}, Genres: g("Drama")},
	{Title: "Drive-In at Echo Valley", Year: 2013, Source: fhdH264, GB: 11.9, Minutes: 102, Added: 930, Seen: seen{jo: 400}, Genres: g("Comedy")},
	{Title: "Marigold Junction", Year: 2016, Source: fhdH264, GB: 10.1, Minutes: 99, Added: 720, Seen: seen{sam: 365}, Genres: g("Romance")},
	{Title: "The Snow Fox of Brindle Fell", Year: 2020, Source: fhdHEVC, GB: 3.8, Minutes: 86, Added: 390, Seen: seen{robin: 30}, Genres: g("Animation", "Family"), Tags: g("kids", "christmas")},
	{Title: "Driftwood Choir", Year: 2019, Source: fhdH264, GB: 12.2, Minutes: 111, Added: 470, Seen: seen{alex: 250, sam: 249}, Genres: g("Music", "Drama")},
	{Title: "Ashcombe Lane", Year: 2022, Source: fhdHEVC, GB: 5.1, Minutes: 123, Added: 150, Genres: g("Thriller")},
	{Title: "The Borrowed Coat", Year: 2014, Source: fhdH264, GB: 9.9, Minutes: 95, Added: 800, Seen: seen{robin: 510}, Genres: g("Drama")},
	{Title: "Glasshouse Summer", Year: 2017, Source: fhdH264, GB: 13.7, Minutes: 106, Added: 560, Seen: seen{sam: 30, alex: 29}, Genres: g("Comedy", "Romance")},
}

// binge is a stretch of a season one user watched: episodes 1 to Through,
// the last one Days ago and each earlier one a day before that.
type binge struct {
	User    string
	Season  int
	Through int
	Days    int
}

type show struct {
	Name    string
	Year    int
	Source  source
	Minutes int
	MinGB   float64 // episode sizes vary between MinGB and MaxGB
	MaxGB   float64
	Added   int
	Genres  []string
	Tags    []string
	Seasons [][]string // episode titles per season
	Watched []binge
	// Odd holds episodes ("S01E04") whose file differs from the rest.
	Odd map[string]source
}

var shows = []show{
	{
		Name: "Harbour Lights", Year: 2021, Source: webH264, Minutes: 52, MinGB: 2.0, MaxGB: 3.2, Added: 640,
		Genres: g("Drama"),
		Seasons: [][]string{
			{"Low Water", "The Pilot Boat", "Fog Signal", "Slack Tide", "Harbour Dues", "The Breakwater", "Night Crossing", "Lights Out"},
			{"Spring Tide", "Moorings", "The Salvage", "Dead Reckoning", "Winter Berth", "Ebb", "The Long Swell", "Landfall"},
		},
		Watched: []binge{{sam, 1, 8, 380}, {alex, 1, 8, 378}, {sam, 2, 8, 120}, {alex, 2, 3, 60}},
	},
	{
		Name: "The Long Acre", Year: 2018, Source: webH264, Minutes: 58, MinGB: 2.8, MaxGB: 3.9, Added: 900,
		Genres: g("Drama", "History"),
		Seasons: [][]string{
			{"Plough Monday", "Hedge and Ditch", "The Tithe Barn", "Harvest Home", "Frost Fair", "Candlemas"},
			{"Lady Day", "Common Rights", "The Enclosure", "Hay Time", "The Hiring Fair", "Michaelmas"},
		},
		Watched: []binge{{robin, 1, 6, 500}, {robin, 2, 6, 95}},
		Odd:     map[string]source{"S01E04": interlaced},
	},
	{
		Name: "Quiet Frequencies", Year: 2023, Source: webHEVC, Minutes: 26, MinGB: 1.0, MaxGB: 1.5, Added: 210,
		Genres: g("Animation", "Family"), Tags: g("kids"),
		Seasons: [][]string{
			{"Static", "The Lighthouse Band", "Pip Hears a Hum", "Radio Garden", "Echo Echo", "The Night Shift", "Crystal Set", "Dead Air", "Short Wave", "Sign Off"},
			{"New Channel", "Feedback", "The Signal Tower", "Morse Code Picnic", "Tuning In", "Fade Out", "The Storm Broadcast", "Long Wave", "Carrier", "Last Call"},
		},
		Watched: []binge{{robin, 1, 10, 40}, {robin, 2, 4, 3}, {jo, 1, 10, 200}},
	},
}

// collection is a Jellyfin box set of films, by title.
type collection struct {
	Name   string
	Titles []string
}

var collections = []collection{
	{Name: "The Halcyon Loop Collection", Titles: []string{"Halcyon Loop", "Halcyon Loop: Drift", "Halcyon Loop: Landfall"}},
	{Name: "Sunday Afternoons", Titles: []string{"Otterburn and the Clockwork Fox", "Starling Parade", "Spinning Top Island", "The Snow Fox of Brindle Fell"}},
}

// result is how a past job ended.
type result string

const (
	done      result = "complete"
	restored  result = "complete, then restored"
	tooSmall  result = "skipped: saving too small"
	failed    result = "failed"
	cancelled result = "cancelled"
)

// pastJob is one History row. Item is a film title or "Series S01E02".
// Days is when it finished. Speed is the encoding speed (1.0 is real
// time); 0 uses a typical software x265 speed.
type pastJob struct {
	Item   string
	Result result
	Days   float64
	Speed  float64
}

// history is listed oldest first. Jobs that finished within the backup
// period (7 days) still have their backup; older ones have expired.
var history = []pastJob{
	{Item: "The Cartographer's Daughter", Result: done, Days: 41.5, Speed: 0.58},
	{Item: "Halcyon Loop", Result: done, Days: 35.2, Speed: 0.62},
	{Item: "The Glass Orchard", Result: done, Days: 27.1, Speed: 0.66},
	{Item: "Salt and Cinder", Result: done, Days: 19.4, Speed: 0.71},
	{Item: "The Copperline Job", Result: restored, Days: 16.3, Speed: 0.69},
	{Item: "Harbour Lights S01E01", Result: done, Days: 14.2, Speed: 2.1},
	{Item: "Halcyon Loop: Drift", Result: cancelled, Days: 12.0},
	{Item: "Harbour Lights S01E02", Result: done, Days: 11.3, Speed: 2.3},
	{Item: "Harbour Lights S01E03", Result: done, Days: 11.2, Speed: 2.2},
	{Item: "The Lantern Keeper", Result: failed, Days: 9.1, Speed: 0.64},
	{Item: "Harbour Lights S01E04", Result: done, Days: 6.2, Speed: 2.4},
	{Item: "Harbour Lights S01E05", Result: tooSmall, Days: 6.1, Speed: 2.2},
	{Item: "Northbound Freight", Result: done, Days: 4.3, Speed: 0.61},
	{Item: "A Map of Small Hours", Result: done, Days: 1.6, Speed: 0.67},
}

// queued are waiting in the Queue, in the order they were added. The first
// was queued by hand with Optimise now, so it runs first.
var queued = []string{
	"The Long Acre S02E01",
	"Twelve Pale Lanterns",
	"Halcyon Loop: Drift",
	"Harbour Lights S01E06",
	"Harbour Lights S01E07",
	"Harbour Lights S01E08",
	"Quiet Frequencies S01E01",
	"Quiet Frequencies S01E02",
}

// running is the queued item shown encoding with -running.
const running = "Twelve Pale Lanterns"

// Fake free space on the media disk, for the Queue's space outlook.
const freeSpaceTB = 1.8

func g(s ...string) []string { return s }
