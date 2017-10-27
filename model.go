package pto3

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-pg/pg"
	"github.com/go-pg/pg/orm"
)

// Observation data model for PTO3 obs and query
// including PostgreSQL object-relational mapping

// Time format for ISO8601 without timezone (everything is always UTC)
const ISO8601Format = "2006-01-02T15:04:05"

type Condition struct {
	ID   int
	Name string
}

func (c *Condition) InsertOnce(db orm.DB) error {
	if c.ID == 0 {
		_, err := db.Model(c).
			Column("id").
			Where("name=?name").
			Returning("id").
			SelectOrInsert()
		if err != nil {
			return err
		}
	}
	return nil
}

// ConditionsByName returns a slice of conditions matching a condition name.
// If a single condition name is given, returns that condition (with ID). If a
// wildcard name is given, returns all conditions (with ID) matching the
// wildcard.
func ConditionsByName(name string, db orm.DB) ([]Condition, error) {
	panic("ConditionsByName() not yet implemented")
	return nil, nil
}

type Path struct {
	ID     int
	String string
}

func (p *Path) InsertOnce(db orm.DB) error {
	if p.ID == 0 {
		_, err := db.Model(p).
			Column("id").
			Where("string=?string").
			Returning("id").
			SelectOrInsert()
		if err != nil {
			return err
		}
	}
	return nil
}

type ObservationSet struct {
	ID       int
	Sources  []string `pg:",array"`
	Analyzer string
	Metadata map[string]string
	datalink string
	link     string
	count    int
}

// MarshalJSON turns this observation set into a JSON observation set metadata
// object suitable for use with the PTO API or as a line in an Observation Set
// File.
func (set *ObservationSet) MarshalJSON() ([]byte, error) {
	jmap := make(map[string]interface{})

	jmap["_sources"] = set.Sources
	jmap["_analyzer"] = set.Analyzer

	if set.link != "" {
		jmap["__link"] = set.link
	}

	if set.datalink != "" {
		jmap["__data"] = set.datalink
	}

	if set.count != 0 {
		jmap["__obs_count"] = set.count
	}

	for k, v := range set.Metadata {
		jmap[k] = v
	}

	return json.Marshal(jmap)
}

// UnmarshalJSON fills in an observation set from a JSON observation set
// metadata object suitable for use with the PTO API.
func (set *ObservationSet) UnmarshalJSON(b []byte) error {
	set.Metadata = make(map[string]string)

	var jmap map[string]interface{}
	err := json.Unmarshal(b, &jmap)
	if err != nil {
		return err
	}

	// zero ID, it will be assigned on insertion or from the
	set.ID = 0

	var ok bool
	for k, v := range jmap {
		if k == "_sources" {
			set.Sources, ok = AsStringArray(v)
			if !ok {
				return errors.New("_sources not a string array")
			}
		} else if k == "_analyzer" {
			set.Analyzer = AsString(v)
		} else if strings.HasPrefix(k, "__") {
			// Ignore all (incoming) __ keys instead of stuffing them in metadata
		} else {
			set.Metadata[k] = AsString(v)
		}
	}

	// make sure we got values for everything
	if set.Sources == nil {
		return errors.New("ObservationSet missing _sources")
	}

	if set.Analyzer == "" {
		return errors.New("ObservationSet missing _analyzer")
	}

	return nil
}

func (set *ObservationSet) Insert(db orm.DB, force bool) error {
	if force {
		set.ID = 0
	}
	if set.ID == 0 {
		return db.Insert(set)
	} else {
		return nil
	}
}

func (set *ObservationSet) Update(db orm.DB) error {
	return db.Update(set)
}

func (set *ObservationSet) LinkVia(baseurl *url.URL) {
	seturl, _ := url.Parse(fmt.Sprintf("obs/%016x", set.ID))
	set.link = baseurl.ResolveReference(seturl).String()
	dataurl, _ := url.Parse(fmt.Sprintf("obs/%016x/data", set.ID))
	set.datalink = baseurl.ResolveReference(dataurl).String()
}

func (set *ObservationSet) CountObservations(db orm.DB) int {
	if set.count == 0 {
		set.count, _ = db.Model(&Observation{}).Where("set_id = ?", set.ID).Count()
	}
	return set.count
}

type Observation struct {
	ID          int
	SetID       int
	Set         *ObservationSet
	Start       time.Time
	End         time.Time
	PathID      int
	Path        *Path
	ConditionID int
	Condition   *Condition
	Value       int
}

// MarshalJSON turns this observation into a JSON array suitable for use as a
// line in an Observation Set File.
func (obs *Observation) MarshalJSON() ([]byte, error) {
	jslice := []interface{}{
		obs.SetID,
		obs.Start.UTC().Format(time.RFC3339),
		obs.End.UTC().Format(time.RFC3339),
		obs.Path.String,
		obs.Condition.Name,
	}

	if obs.Value != 0 {
		jslice = append(jslice, obs.Value)
	}

	return json.Marshal(&jslice)
}

// UnmarshalJSON fills in this observation from a JSON array line in an
// Observation Set File.
func (obs *Observation) UnmarshalJSON(b []byte) error {
	var jslice []interface{}

	err := json.Unmarshal(b, &jslice)
	if err != nil {
		return err
	}

	if len(jslice) < 5 {
		return errors.New("Observation requires at least five elements")
	}

	obs.ID = 0
	obs.SetID, err = strconv.Atoi(AsString(jslice[0])) // fill in Set ID, will be ignored by force insert

	obs.Start, err = time.Parse(time.RFC3339, AsString(jslice[1]))
	if err != nil {
		return err
	}
	obs.End, err = time.Parse(time.RFC3339, AsString(jslice[2]))
	if err != nil {
		return err
	}

	obs.Path = &Path{String: AsString(jslice[3])}
	obs.Condition = &Condition{Name: AsString(jslice[4])}

	if len(jslice) >= 6 {
		obs.Value, err = strconv.Atoi(AsString(jslice[5]))
		if err != nil {
			return err
		}
	}

	return nil
}

func (obs *Observation) InsertInSet(db orm.DB, set *ObservationSet) error {
	if err := obs.Path.InsertOnce(db); err != nil {
		return err
	}
	obs.PathID = obs.Path.ID

	if err := obs.Condition.InsertOnce(db); err != nil {
		return err
	}
	obs.ConditionID = obs.Condition.ID

	obs.Set = set
	if err := obs.Set.Insert(db, false); err != nil {
		return err
	}
	obs.SetID = obs.Set.ID

	return db.Insert(obs)
}

func WriteObservations(obsdat []Observation, out io.Writer) error {
	for _, obs := range obsdat {
		b, err := json.Marshal(&obs)
		if err != nil {
			return err
		}
		_, err = out.Write(b)
		if err != nil {
			return err
		}
		_, err = out.Write([]byte("\n"))
		if err != nil {
			return err
		}
	}
	return nil
}

func MarshalObservations(obsdat []Observation) ([]byte, error) {
	var out bytes.Buffer
	err := WriteObservations(obsdat, &out)
	if err != nil {
		return nil, err
	}
	return out.Bytes(), err
}

func ReadObservations(in io.Reader) ([]Observation, error) {
	sin := bufio.NewScanner(in)
	out := make([]Observation, 0)
	var obs Observation
	for sin.Scan() {
		if err := json.Unmarshal([]byte(sin.Text()), &obs); err != nil {
			return nil, err
		}
		out = append(out, obs)
	}
	return out, nil
}

func UnmarshalObservations(in []byte) ([]Observation, error) {
	return ReadObservations(bytes.NewBuffer(in))
}

// CreateTables insures that the tables used by the ORM exist in the given
// database. This is used for testing, and the (not yet implemented) ptodb init
// command.
func CreateTables(db *pg.DB) error {
	opts := orm.CreateTableOptions{
		IfNotExists:   true,
		FKConstraints: true,
	}

	return db.RunInTransaction(func(tx *pg.Tx) error {
		if err := db.CreateTable(&Condition{}, &opts); err != nil {
			return err
		}

		if err := db.CreateTable(&Path{}, &opts); err != nil {
			return err
		}

		if err := db.CreateTable(&ObservationSet{}, &opts); err != nil {
			return err
		}

		if err := db.CreateTable(&Observation{}, &opts); err != nil {
			return err
		}

		return nil
	})
}

// DropTables removes the tables used by the ORM from the database. Use this for
// testing only, please.
func DropTables(db *pg.DB) error {
	return db.RunInTransaction(func(tx *pg.Tx) error {
		if err := db.DropTable(&Observation{}, nil); err != nil {
			return err
		}

		if err := db.DropTable(&ObservationSet{}, nil); err != nil {
			return err
		}

		if err := db.DropTable(&Condition{}, nil); err != nil {
			return err
		}

		if err := db.DropTable(&Path{}, nil); err != nil {
			return err
		}

		return nil
	})
}