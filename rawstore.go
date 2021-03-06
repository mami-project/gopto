package pto3

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CampaignMetadataFilename is the name of each campaign metadata file in each campaign directory
const CampaignMetadataFilename = "__pto_campaign_metadata.json"

// FileMetadataSuffix is the suffix on each metadata file on disk
const FileMetadataSuffix = ".pto_file_metadata.json"

// DeletionTagSuffix is the suffix on a deletion tag on disk
const DeletionTagSuffix = ".pto_file_delete_me"

// DataRelativeURL is the path relative to each file metadata path for content access
var DataRelativeURL *url.URL

func init() {
	DataRelativeURL, _ = url.Parse("data")
}

// RawMetadata represents metadata for a raw data object (file or campaign)
type RawMetadata struct {
	// Parent metadata object (campaign metadata, for files)
	Parent *RawMetadata
	// Name of filetype
	filetype string
	// Owner identifier
	owner string
	// Start time for records in the file
	timeStart *time.Time
	// End time for records in the file
	timeEnd *time.Time
	// Arbitrary metadata
	Metadata map[string]string
	// Link to data object
	datalink string
	// Size of data object
	datasize int
	// File creation time
	creatime *time.Time
	// Metadata modification time
	modtime *time.Time
}

func (md *RawMetadata) Keys(inherit bool) []string {
	keymap := make(map[string]struct{})

	for k := range md.Metadata {
		if !strings.HasPrefix(k, "__") {
			keymap[k] = struct{}{}
		}
	}

	if inherit && md.Parent != nil {
		for k := range md.Parent.Metadata {
			if !strings.HasPrefix(k, "__") {
				keymap[k] = struct{}{}
			}
		}
	}

	out := make([]string, len(keymap))
	i := 0
	for k := range keymap {
		out[i] = k
		i++
	}

	return out
}

// Filetype returns the filetype name associated with a given metadata object,
// or inherited from its parent.
func (md *RawMetadata) Filetype(inherit bool) string {
	if md.filetype == "" && inherit && md.Parent != nil {
		return md.Parent.filetype
	} else {
		return md.filetype
	}
}

// Owner returns the owner name associated with a given metadata object,
// or inherited from its parent.
func (md *RawMetadata) Owner(inherit bool) string {
	if md.owner == "" && inherit && md.Parent != nil {
		return md.Parent.owner
	} else {
		return md.owner
	}
}

// TimeStart returns the start time associated with a given metadata object,
// or inherited from its parent.
func (md *RawMetadata) TimeStart(inherit bool) *time.Time {
	if md.timeStart == nil && inherit && md.Parent != nil {
		return md.Parent.timeStart
	} else {
		return md.timeStart
	}
}

// TimeEnd returns the end time associated with a given metadata object,
// or inherited from its parent.
func (md *RawMetadata) TimeEnd(inherit bool) *time.Time {
	if md.timeEnd == nil && inherit && md.Parent != nil {
		return md.Parent.timeEnd
	} else {
		return md.timeEnd
	}
}

func (md *RawMetadata) Get(k string, inherit bool) string {
	out := md.Metadata[k]
	if out == "" && inherit && md.Parent != nil {
		out = md.Parent.Metadata[k]
	}
	return out
}

func (md *RawMetadata) CreationTime() *time.Time {
	return md.creatime
}

func (md *RawMetadata) ModificationTime() *time.Time {
	return md.modtime
}

// DumpJSONObject serializes a RawMetadata object to JSON. If inherit is true,
// this inherits data and metadata items from the parent; if false, it only
// dumps information in this object itself.
func (md *RawMetadata) DumpJSONObject(inherit bool) ([]byte, error) {
	jmap := make(map[string]interface{})

	// dump required keys
	ft := md.Filetype(inherit)
	if ft != "" {
		jmap["_file_type"] = ft
	}

	ow := md.Owner(inherit)
	if ow != "" {
		jmap["_owner"] = ow
	}

	ts := md.TimeStart(inherit)
	if ts != nil {
		jmap["_time_start"] = ts.Format(time.RFC3339)
	}

	te := md.TimeEnd(inherit)
	if te != nil {
		jmap["_time_end"] = te.Format(time.RFC3339)
	}

	// dump derived keys (not inheritable)
	if md.datalink != "" {
		jmap["__data"] = md.datalink
	}

	if md.datasize != 0 {
		jmap["__data_size"] = md.datasize
	}

	if md.creatime != nil {
		jmap["__created"] = md.creatime.Format(time.RFC3339)
	}

	if md.modtime != nil {
		jmap["__modified"] = md.modtime.Format(time.RFC3339)
	}

	// dump arbitrary keys
	for _, k := range md.Keys(inherit) {
		jmap[k] = md.Get(k, inherit)
	}

	return json.Marshal(jmap)
}

// MarshalJSON serializes a RawMetadata object to JSON. All values inherited
// from the parent, if present, are also serialized see DumpJSONObject for
// control over inheritance.
func (md *RawMetadata) MarshalJSON() ([]byte, error) {
	// by default, serialize object with all inherited information
	return md.DumpJSONObject(true)
}

// UnmarshalJSON fills in a RawMetadata object from JSON.
func (md *RawMetadata) UnmarshalJSON(b []byte) error {
	md.Metadata = make(map[string]string)

	var jmap map[string]interface{}

	if err := json.Unmarshal(b, &jmap); err != nil {
		return PTOWrapError(err)
	}

	var err error
	for k, v := range jmap {
		if k == "_file_type" {
			md.filetype = AsString(v)
		} else if k == "_owner" {
			md.owner = AsString(v)
		} else if k == "_time_start" {
			var t time.Time
			if t, err = AsTime(v); err != nil {
				return PTOWrapError(err)
			}
			md.timeStart = &t
		} else if k == "_time_end" {
			var t time.Time
			if t, err = AsTime(v); err != nil {
				return PTOWrapError(err)
			}
			md.timeEnd = &t
		} else if strings.HasPrefix(k, "__") {
			// Ignore all (incoming) __ keys instead of stuffing them in metadata
		} else {
			md.Metadata[k] = AsString(v)
		}
	}

	return nil
}

// writeToFile writes this RawMetadata object as JSON to a file.
func (md *RawMetadata) writeToFile(pathname string) error {
	b, err := md.DumpJSONObject(false)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(pathname, b, 0644)
}

// validate returns nil if the metadata is valid (i.e., it or its parent has all required keys), or an error if not
func (md *RawMetadata) validate(isCampaign bool) error {
	// everything needs an error
	if md.Owner(true) == "" {
		return PTOMissingMetadataError("_owner")
	}

	// short circuit file-only checks
	if isCampaign {
		return nil
	}

	if md.Filetype(true) == "" {
		return PTOMissingMetadataError("_file_type")
	}

	if md.TimeStart(true) == nil {
		return PTOMissingMetadataError("_time_start")
	}

	if md.TimeEnd(true) == nil {
		return PTOMissingMetadataError("_time_end")
	}

	return nil
}

// RawMetadataFromReader reads metadata for a raw data file from a stream. It
// creates a new RawMetadata object bound to an optional parent.
func RawMetadataFromReader(r io.Reader, parent *RawMetadata) (*RawMetadata, error) {
	var md RawMetadata

	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, PTOWrapError(err)
	}

	if err = json.Unmarshal(b, &md); err != nil {
		return nil, PTOWrapError(err)
	}

	// link to campaign metadata for inheritance
	md.Parent = parent
	return &md, nil
}

// RawMetadataFromFile reads metadata for a raw data file from a file. It
// creates a new RawMetadata object bound to an optional parent.
func RawMetadataFromFile(pathname string, parent *RawMetadata) (*RawMetadata, error) {
	f, err := os.Open(pathname)
	if err != nil {
		return nil, PTOWrapError(err)
	}
	defer f.Close()
	return RawMetadataFromReader(f, parent)
}

// RawFiletype encapsulates a filetype in the raw data store
type RawFiletype struct {
	// PTO filetype name
	Filetype string `json:"file_type"`
	// Associated MIME type
	ContentType string `json:"mime_type"`
}

// FIXME reconsider design of RawFiletype

// Campaign encapsulates a single campaign in a raw data store,
// and caches metadata for the campaign and files within it.
type Campaign struct {
	// application configuration
	config *PTOConfiguration

	// path to campaign directory
	path string

	// requires metadata reload
	stale bool

	// campaign metadata cache
	campaignMetadata *RawMetadata

	// file metadata cache; keys of this define known filenames
	fileMetadata map[string]*RawMetadata

	// lock on metadata structures
	lock sync.RWMutex
}

// newCampaign creates a new campaign object bound the path of a directory on
// disk containing the campaign's files. If a pointer to metadata is given, it
// creates a new campaign directory on disk with the given metadata. Error can
// be ignored if metadata is nil.
func newCampaign(config *PTOConfiguration, name string, md *RawMetadata) (*Campaign, error) {

	cam := &Campaign{
		config:       config,
		path:         filepath.Join(config.RawRoot, name),
		stale:        true,
		fileMetadata: make(map[string]*RawMetadata),
	}

	// metadata means try to create new campaign
	if md != nil {

		// okay, we're trying to make a new campaign. first, make sure campaign metadata is ok
		if err := md.validate(true); err != nil {
			return nil, err
		}

		// then check to see if the campaign directory exists
		_, err := os.Stat(cam.path)
		if (err == nil) || !os.IsNotExist(err) {
			return nil, PTOExistsError("campaign", name)
		}

		// create directory
		if err := os.Mkdir(cam.path, 0755); err != nil {
			return nil, PTOWrapError(err)
		}

		// write metadata to campaign metadata file
		if err := md.writeToFile(filepath.Join(cam.path, CampaignMetadataFilename)); err != nil {
			return nil, err
		}

		// and force a rescan
		if err := cam.reloadMetadata(true); err != nil {
			return nil, err
		}

	}

	return cam, nil

}

// reloadMetadata reloads the metadata for this campaign and its files from disk
func (cam *Campaign) reloadMetadata(force bool) error {
	var err error

	cam.lock.Lock()
	defer cam.lock.Unlock()

	// skip if not stale
	if !force && !cam.stale {
		return nil
	}

	// load the campaign metadata file
	cam.campaignMetadata, err = RawMetadataFromFile(filepath.Join(cam.path, CampaignMetadataFilename), nil)
	if err != nil {
		return err
	}

	// now scan directory and load each metadata file
	direntries, err := ioutil.ReadDir(cam.path)
	for _, direntry := range direntries {
		metafilename := direntry.Name()
		if strings.HasSuffix(metafilename, FileMetadataSuffix) {
			linkname := metafilename[0 : len(metafilename)-len(FileMetadataSuffix)]
			cam.fileMetadata[linkname], err =
				RawMetadataFromFile(filepath.Join(cam.path, metafilename), cam.campaignMetadata)
			if err != nil {
				return err
			}
			// update virtual metadata after load FIXME do better than this?
			if err := cam.updateFileVirtualMetadata(linkname); err != nil {
				return err
			}
		}
	}

	// everything loaded, mark not stale and return no error
	cam.stale = false
	return nil
}

// unloadMetadata allows a campaign's metadata to be garbage-collected, requiring reload on access.
func (cam *Campaign) unloadMetadata() {
	cam.lock.Lock()
	defer cam.lock.Unlock()

	cam.campaignMetadata = nil
	cam.fileMetadata = nil
	cam.stale = true
}

// GetCampaignMetadata returns the metadata for this campaign.
func (cam *Campaign) GetCampaignMetadata() (*RawMetadata, error) {
	// reload if stale
	err := cam.reloadMetadata(false)
	if err != nil {
		return nil, err
	}

	return cam.campaignMetadata, nil
}

// PutCampaignMetadata overwrites the metadata for this campaign with the given metadata.
func (cam *Campaign) PutCampaignMetadata(md *RawMetadata) error {
	cam.lock.Lock()
	defer cam.lock.Unlock()

	// make sure campaign metadata is ok
	if err := md.validate(true); err != nil {
		return err
	}

	// write to campaign metadata file
	if err := md.writeToFile(filepath.Join(cam.path, CampaignMetadataFilename)); err != nil {
		return err
	}

	// update metadata cache
	cam.campaignMetadata = md
	return nil
}

// FileNames returns a sorted  list of filenames currently in the campaign.
func (cam *Campaign) FileNames() ([]string, error) {
	// reload if stale
	err := cam.reloadMetadata(false)
	if err != nil {
		return nil, err
	}

	cam.lock.RLock()
	defer cam.lock.RUnlock()
	out := make([]string, len(cam.fileMetadata))
	i := 0
	for filename := range cam.fileMetadata {
		out[i] = filename
		i++
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out, nil
}

// GetFileMetadata retrieves metadata for a file in this campaign given a file name.
func (cam *Campaign) GetFileMetadata(filename string) (*RawMetadata, error) {
	// reload if stale
	err := cam.reloadMetadata(false)
	if err != nil {
		return nil, err
	}

	// check for file metadata
	filemd, ok := cam.fileMetadata[filename]
	if !ok {
		return nil, PTONotFoundError("file", filename)
	}

	return filemd, nil
}

// updateFileVirtualMetadata fills in the system virtual metadata for a file.
// Not concurrency safe: caller must hold the campaign lock.
func (cam *Campaign) updateFileVirtualMetadata(filename string) error {
	// get file metadata
	md, ok := cam.fileMetadata[filename]
	if !ok {
		return PTONotFoundError("file", filename)
	}

	// get file size and creation time
	// file creation time is modification time of the datafile,
	// since datafiles are immutable.
	datafi, err := os.Stat(filepath.Join(cam.path, filename))
	if err == nil {
		md.datasize = int(datafi.Size())
		modtime := datafi.ModTime()
		md.creatime = &modtime
	} else if os.IsNotExist(err) {
		md.datasize = 0
		md.creatime = nil
	} else {
		return err
	}

	// get modification time (from metadata file modification time)
	metafi, err := os.Stat(filepath.Join(cam.path, filename+FileMetadataSuffix))
	if err == nil {
		modtime := metafi.ModTime()
		md.modtime = &modtime

		if md.creatime == nil {
			// creation time is the same as modification time if there is no datafile yet
			md.creatime = md.modtime
		} else if md.creatime.Sub(*md.modtime) > 0 {
			// modification time cannot be before creation time
			md.modtime = md.creatime
		}
	} else {
		return err
	}

	// generate data path
	md.datalink, err = cam.config.LinkTo("raw/" + filepath.Base(cam.path) + "/" + filename + "/data")
	if err != nil {
		return err
	}

	return nil
}

// PutFileMetadata overwrites the metadata in this campaign with the given metadata.
func (cam *Campaign) PutFileMetadata(filename string, md *RawMetadata) error {
	// reload if stale
	err := cam.reloadMetadata(false)
	if err != nil {
		return err
	}

	cam.lock.Lock()
	defer cam.lock.Unlock()

	// inherit from campaign
	md.Parent = cam.campaignMetadata

	// ensure we have a filetype
	if md.Filetype(true) == "" {
		return PTOMissingMetadataError("_file_type")
	}

	// write to file metadata file
	err = md.writeToFile(filepath.Join(cam.path, filename+FileMetadataSuffix))
	if err != nil {
		return err
	}

	// update metadata cache
	cam.fileMetadata[filename] = md

	// and update virtuals
	return cam.updateFileVirtualMetadata(filename)
}

// GetFiletype returns the filetype associated with a given file in this campaign.
func (cam *Campaign) GetFiletype(filename string) *RawFiletype {
	// reload if stale
	err := cam.reloadMetadata(false)
	if err != nil {
		return nil
	}

	md, ok := cam.fileMetadata[filename]
	if !ok {
		return nil
	}

	ftname := md.Filetype(true)
	ctype, ok := cam.config.ContentTypes[ftname]
	if !ok {
		return nil
	}

	return &RawFiletype{ftname, ctype}
}

// ReadFileData opens and returns the data file associated with a filename on this campaign for reading.
func (cam *Campaign) ReadFileData(filename string) (*os.File, error) {
	// build a local filesystem path and validate it
	rawpath := filepath.Clean(filepath.Join(cam.path, filename))
	if pathok, _ := filepath.Match(filepath.Join(cam.path, "*"), rawpath); !pathok {
		return nil, PTOErrorf("path %s is not ok", rawpath).StatusIs(http.StatusInternalServerError)
	}

	// open the file
	return os.Open(rawpath)
}

// ReadFileDataToStream copies data from the data file associated with a
// filename on this campaign to a given writer.
func (cam *Campaign) ReadFileDataToStream(filename string, out io.Writer) error {
	in, err := cam.ReadFileData(filename)
	if err != nil {
		return err
	}
	defer in.Close()

	// now copy to the writer until EOF
	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return nil
}

// WriteDataFile creates, open and returns the data file associated with a
// filename on this campaign for writing.If force is true, replaces the data
// file if it exists; otherwise, returns an error if the data file exists.
func (cam *Campaign) WriteFileData(filename string, force bool) (*os.File, error) {
	// build a local filesystem path and validate it
	rawpath := filepath.Clean(filepath.Join(cam.path, filename))
	if pathok, _ := filepath.Match(filepath.Join(cam.path, "*"), rawpath); !pathok {
		return nil, PTOErrorf("path %s is not ok", rawpath).StatusIs(http.StatusInternalServerError)
	}

	// ensure file isn't there unless we're forcing overwrite
	if !force {
		_, err := os.Stat(rawpath)
		if (err == nil) || !os.IsNotExist(err) {
			return nil, PTOExistsError("file", filename)
		}
	}

	// create file to write to
	return os.Create(rawpath)
}

// WriteFileDataFromStream copies data from a given reader to the data file
// associated with a filename on this campaign. If force is true, replaces the
// data file if it exists; otherwise, returns an error if the data file exists.
func (cam *Campaign) WriteFileDataFromStream(filename string, force bool, in io.Reader) error {
	out, err := cam.WriteFileData(filename, force)
	if err != nil {
		return err
	}
	defer out.Close()

	// now copy from the reader until EOF
	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	// flush file to disk
	if err := out.Sync(); err != nil {
		return PTOWrapError(err)
	}

	// update virtual metadata, as the underlying file size will have changed
	cam.lock.Lock()
	defer cam.lock.Unlock()
	return cam.updateFileVirtualMetadata(filename)
}

// A RawDataStore encapsulates a pile of PTO data and metadata files as a set of
// campaigns.
type RawDataStore struct {
	// application configuration
	config *PTOConfiguration

	// base path
	path string

	// lock on campaign cache
	lock sync.RWMutex

	// campaign cache
	campaigns map[string]*Campaign
}

// ScanCampaigns updates the campaign cache in RawDataStore to reflect the
// current state of the files on disk.
func (rds *RawDataStore) ScanCampaigns() error {
	rds.lock.Lock()
	defer rds.lock.Unlock()

	rds.campaigns = make(map[string]*Campaign)

	direntries, err := ioutil.ReadDir(rds.path)

	if err != nil {
		return PTOWrapError(err)
	}

	for _, direntry := range direntries {
		if direntry.IsDir() {

			// look for a metadata file
			mdpath := filepath.Join(rds.path, direntry.Name(), CampaignMetadataFilename)
			_, err := os.Stat(mdpath)
			if err != nil {
				if os.IsNotExist(err) {
					log.Printf("Missing campaign metadata file %s", mdpath)
					continue // no metadata file means we don't care about this directory
				} else {
					return PTOWrapError(err) // something else broke. die.
				}
			}

			// create a new (stale) campaign
			cam, _ := newCampaign(rds.config, direntry.Name(), nil)
			rds.campaigns[direntry.Name()] = cam
		}
	}

	return nil
}

// CreateCampaign creates a new campaign given a campaign name and initial metadata for the new campaign.
func (rds *RawDataStore) CreateCampaign(camname string, md *RawMetadata) (*Campaign, error) {
	cam, err := newCampaign(rds.config, camname, md)
	if err != nil {
		return nil, err
	}

	err = cam.PutCampaignMetadata(md)
	if err != nil {
		return nil, err
	}

	rds.lock.Lock()
	rds.campaigns[camname] = cam
	rds.lock.Unlock()

	return cam, nil
}

// CampaignForName returns a campaign object for a given name.
func (rds *RawDataStore) CampaignForName(camname string) (*Campaign, error) {
	// die if campaign not found
	cam, ok := rds.campaigns[camname]
	if !ok {
		return nil, PTONotFoundError("campaign", camname)
	}

	return cam, nil
}

func (rds *RawDataStore) CampaignNames() []string {
	// return list of names
	rds.lock.RLock()
	defer rds.lock.RUnlock()
	out := make([]string, len(rds.campaigns))
	i := 0
	for k := range rds.campaigns {
		out[i] = k
		i++
	}
	return out
}

// NewRawDataStore encapsulates a raw data store, given a configuration object
// pointing to a directory containing data and metadata organized into campaigns.
func NewRawDataStore(config *PTOConfiguration) (*RawDataStore, error) {
	rds := RawDataStore{config: config, path: config.RawRoot}

	// scan the directory for campaigns
	if err := rds.ScanCampaigns(); err != nil {
		return nil, err
	}

	return &rds, nil
}
