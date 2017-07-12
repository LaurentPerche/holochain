// Copyright (C) 2013-2017, The MetaCurrency Project (Eric Harris-Braun, Arthur Brock, et. al.)
// Use of this source code is governed by GPLv3 found in the LICENSE file
//----------------------------------------------------------------------------------------
//
package holochain

import (
	"encoding/json"
	"errors"
	"fmt"
	peer "github.com/libp2p/go-libp2p-peer"
	entangler "github.com/metacurrency/holochain/entangler"
	"reflect"
	"time"
)

type ArgType int8

// these constants define the argument types for actions, i.e. system functions callable
// from within nuclei
const (
	HashArg = iota
	StringArg
	EntryArg // special arg type for entries, can be a string or a hash
	IntArg
	BoolArg
	MapArg
	ToStrArg // special arg type that converts anything to a string, used for the debug action
	ArgsArg  // special arg type for arguments passed to the call action
)

type Arg struct {
	Name     string
	Type     ArgType
	Optional bool
	MapType  reflect.Type
	value    interface{}
}

// Action provides an abstraction for grouping all the aspects of a nucleus function, i.e.
// the initiating actions, receiving them, validation, ribosome generation etc
type Action interface {
	Name() string
	Do(h *Holochain) (response interface{}, err error)
	Receive(dht *DHT, msg *Message) (response interface{}, err error)
	Args() []Arg
}

// CommittingAction provides an abstraction for grouping actions which carry Entry data
type CommittingAction interface {
	Name() string
	Do(h *Holochain) (response interface{}, err error)
	SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error)
	Receive(dht *DHT, msg *Message) (response interface{}, err error)
	CheckValidationRequest(def *EntryDef) (err error)
	Args() []Arg
	EntryType() string
	Entry() Entry
}

// ValidatingAction provides an abstraction for grouping all the actions that participate in validation loop
type ValidatingAction interface {
	Name() string
	Do(h *Holochain) (response interface{}, err error)
	SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error)
	Receive(dht *DHT, msg *Message) (response interface{}, err error)
	CheckValidationRequest(def *EntryDef) (err error)
	Args() []Arg
}

var NonDHTAction error = errors.New("Not a DHT action")
var NonCallableAction error = errors.New("Not a callable action")

func prepareSources(sources []peer.ID) (srcs []string) {
	srcs = make([]string, 0)
	for _, s := range sources {
		srcs = append(srcs, peer.IDB58Encode(s))
	}
	return
}

// ValidateAction runs the different phases of validating an action
func (h *Holochain) ValidateAction(a ValidatingAction, entryType string, pkg *Package, sources []peer.ID) (d *EntryDef, err error) {
	switch entryType {
	case DNAEntryType:
		//		panic("attempt to get validation response for DNA")
	case KeyEntryType:
		//		validate the public key?
	case AgentEntryType:
		//		validate the Agent Entry?
	default:

		// validation actions for application defined entry types

		var z *Zome
		z, d, err = h.GetEntryDef(entryType)
		if err != nil {
			return
		}

		var vpkg *ValidationPackage
		vpkg, err = MakeValidationPackage(h, pkg)
		if err != nil {
			return
		}

		// run the action's system level validations
		err = a.SysValidation(h, d, sources)
		if err != nil {
			Debugf("Sys ValidateAction(%T) err:%v\n", a, err)
			return
		}

		// run the action's app level validations
		var n Ribosome
		n, err = z.MakeRibosome(h)
		if err != nil {
			return
		}

		err = n.ValidateAction(a, d, vpkg, prepareSources(sources))
		if err != nil {
			Debugf("Ribosome ValidateAction(%T) err:%v\n", a, err)
		}
	}
	return
}

// GetValidationResponse check the validation request and builds the validation package based
// on the app's requirements
func (h *Holochain) GetValidationResponse(a ValidatingAction, hash Hash) (resp ValidateResponse, err error) {
	var entry Entry
	entry, resp.Type, err = h.chain.GetEntry(hash)
	if err == ErrHashNotFound {
		if hash.String() == h.nodeIDStr {
			resp.Type = KeyEntryType
			err = nil
		} else {
			return
		}
	} else if err != nil {
		return
	} else {
		resp.Entry = *(entry.(*GobEntry))
		var hd *Header
		hd, err = h.chain.GetEntryHeader(hash)
		if err != nil {
			return
		}
		resp.Header = *hd
	}
	switch resp.Type {
	case DNAEntryType:
		panic("attempt to get validation response for DNA")
	case KeyEntryType:
		//		resp.Entry = TODO public key goes here
	case AgentEntryType:
		//		resp.Entry = TODO agent block goes here
	default:
		// app defined entry types
		var def *EntryDef
		var z *Zome
		z, def, err = h.GetEntryDef(resp.Type)
		if err != nil {
			return
		}
		err = a.CheckValidationRequest(def)
		if err != nil {
			return
		}

		// get the packaging request from the app
		var n Ribosome
		n, err = z.MakeRibosome(h)
		if err != nil {
			return
		}

		var req PackagingReq
		req, err = n.ValidatePackagingRequest(a, def)
		if err != nil {
			Debugf("Ribosome GetValidationPackage(%T) err:%v\n", a, err)
		}
		resp.Package, err = MakePackage(h, req)
	}
	return
}

// MakeActionFromMessage generates an action from an action protocol messsage
func MakeActionFromMessage(msg *Message) (a Action, err error) {
	var t reflect.Type
	switch msg.Type {
	case APP_MESSAGE:
		a = &ActionSend{}
		t = reflect.TypeOf(AppMsg{})
	case PUT_REQUEST:
		a = &ActionPut{}
		t = reflect.TypeOf(PutReq{})
	case GET_REQUEST:
		a = &ActionGet{}
		t = reflect.TypeOf(GetReq{})
	case MOD_REQUEST:
		a = &ActionMod{}
		t = reflect.TypeOf(ModReq{})
	case DEL_REQUEST:
		a = &ActionDel{}
		t = reflect.TypeOf(DelReq{})
	case LINK_REQUEST:
		a = &ActionLink{}
		t = reflect.TypeOf(LinkReq{})
	case GETLINK_REQUEST:
		a = &ActionGetLink{}
		t = reflect.TypeOf(LinkQuery{})
	default:
		err = fmt.Errorf("message type %d not in holochain-action protocol", int(msg.Type))
	}
	if err == nil && reflect.TypeOf(msg.Body) != t {
		err = fmt.Errorf("Unexpected request body type '%T' in %s request, expecting %v", msg.Body, a.Name(), t)
	}
	return
}

var ErrWrongNargs = errors.New("wrong number of arguments")

func checkArgCount(args []Arg, l int) (err error) {
	var min int
	for _, a := range args {
		if !a.Optional {
			min++
		}
	}
	if l < min || l > len(args) {
		err = ErrWrongNargs
	}
	return
}

func argErr(typeName string, index int, arg Arg) error {
	return fmt.Errorf("argument %d (%s) should be %s", index, arg.Name, typeName)
}

//------------------------------------------------------------
// Property

type ActionProperty struct {
	prop string
}

func NewPropertyAction(prop string) *ActionProperty {
	a := ActionProperty{prop: prop}
	return &a
}

func (a *ActionProperty) Name() string {
	return "property"
}

func (a *ActionProperty) Args() []Arg {
	return []Arg{{Name: "name", Type: StringArg}}
}

func (a *ActionProperty) Do(h *Holochain) (response interface{}, err error) {
	response, err = h.GetProperty(a.prop)
	return
}

//------------------------------------------------------------
// Debug

type ActionDebug struct {
	msg string
}

func NewDebugAction(msg string) *ActionDebug {
	a := ActionDebug{msg: msg}
	return &a
}

func (a *ActionDebug) Name() string {
	return "debug"
}

func (a *ActionDebug) Args() []Arg {
	return []Arg{{Name: "value", Type: ToStrArg}}
}

func (a *ActionDebug) Do(h *Holochain) (response interface{}, err error) {
	h.config.Loggers.App.p(a.msg)
	return
}

//------------------------------------------------------------
// MakeHash

type ActionMakeHash struct {
	entry Entry
}

func NewMakeHashAction(entry Entry) *ActionMakeHash {
	a := ActionMakeHash{entry: entry}
	return &a
}

func (a *ActionMakeHash) Name() string {
	return "makeHash"
}

func (a *ActionMakeHash) Args() []Arg {
	return []Arg{{Name: "entry", Type: EntryArg}}
}

func (a *ActionMakeHash) Do(h *Holochain) (response interface{}, err error) {
	var hash Hash
	hash, err = a.entry.Sum(h.hashSpec)
	if err != nil {
		return
	}
	response = hash
	return
}

//------------------------------------------------------------
// Call

type ActionCall struct {
	zome     string
	function string
	args     interface{}
}

func NewCallAction(zome string, function string, args interface{}) *ActionCall {
	a := ActionCall{zome: zome, function: function, args: args}
	return &a
}

func (a *ActionCall) Name() string {
	return "call"
}

func (a *ActionCall) Args() []Arg {
	return []Arg{{Name: "zome", Type: StringArg}, {Name: "function", Type: StringArg}, {Name: "args", Type: ArgsArg}}
}

func (a *ActionCall) Do(h *Holochain) (response interface{}, err error) {
	response, err = h.Call(a.zome, a.function, a.args, ZOME_EXPOSURE)
	return
}

//------------------------------------------------------------
// Send

type ActionSend struct {
	to  peer.ID
	msg AppMsg
}

func NewSendAction(to peer.ID, msg AppMsg) *ActionSend {
	a := ActionSend{to: to, msg: msg}
	return &a
}

func (a *ActionSend) Name() string {
	return "send"
}

func (a *ActionSend) Args() []Arg {
	return []Arg{{Name: "to", Type: HashArg}, {Name: "msg", Type: MapArg}}
}

func (a *ActionSend) Do(h *Holochain) (response interface{}, err error) {
	var r interface{}
	r, err = h.Send(ActionProtocol, a.to, APP_MESSAGE, a.msg)
	if err == nil {
		response = r.(AppMsg).Body
	}
	return
}

func (a *ActionSend) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	t := msg.Body.(AppMsg)
	var r Ribosome
	r, _, err = dht.h.MakeRibosome(t.ZomeType)
	if err != nil {
		return
	}
	rsp := AppMsg{ZomeType: t.ZomeType}
	rsp.Body, err = r.Receive(peer.IDB58Encode(msg.From), t.Body)
	if err == nil {
		response = rsp
	}
	return
}

//------------------------------------------------------------
// Get

type ActionGet struct {
	req     GetReq
	options *GetOptions
}

func NewGetAction(req GetReq, options *GetOptions) *ActionGet {
	a := ActionGet{req: req, options: options}
	return &a
}

func (a *ActionGet) Name() string {
	return "get"
}

func (a *ActionGet) Args() []Arg {
	return []Arg{{Name: "hash", Type: HashArg}, {Name: "options", Type: MapArg, MapType: reflect.TypeOf(GetOptions{}), Optional: true}}
}

func (a *ActionGet) Do(h *Holochain) (response interface{}, err error) {
	if a.options.Local {
		var entry Entry
		var entryType string
		entry, entryType, err = h.chain.GetEntry(a.req.H)
		if err != nil {
			return
		}
		resp := GetResp{}
		mask := a.options.GetMask
		if (mask & GetMaskEntryType) != 0 {
			resp.EntryType = entryType
		}
		if (mask & GetMaskEntry) != 0 {
			resp.Entry = entry
		}

		response = resp
		return
	}

	response, err = entangler.GetEntangler().Execute(
		func(seg *capnp.Segment) {
			getReq := NewGetRequest(seg)
			getReq.SetHash(a.req.H)
			getReq.SetStatusMask(a.options.StatusMask)
			getReq.SetGetMask(a.options.GetMask)
			return getReq
		}).Get()

	return
}

func (a *ActionGet) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	return
}

func (a *ActionGet) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	var entryData []byte
	//var status int
	req := msg.Body.(GetReq)
	mask := req.GetMask
	if mask == GetMaskDefault {
		mask = GetMaskEntry
	}
	resp := GetResp{}
	var entryType string
	entryData, entryType, resp.Sources, _, err = dht.get(req.H, req.StatusMask, req.GetMask|GetMaskEntryType)
	if (mask & GetMaskEntryType) != 0 {
		resp.EntryType = entryType
	}

	if err == nil {
		if (mask & GetMaskEntry) != 0 {
			switch entryType {
			case DNAEntryType:
				panic("nobody should actually get the DNA!")
			case AgentEntryType:
				fallthrough
			case KeyEntryType:
				var e GobEntry
				e.C = string(entryData)
				resp.Entry = &e
			default:
				var e GobEntry
				err = e.Unmarshal(entryData)
				if err != nil {
					return
				}
				resp.Entry = &e
			}
		}
	} else {
		if err == ErrHashModified {
			resp.FollowHash = string(entryData)
		}
	}
	response = resp
	return
}

// doCommit adds an entry to the local chain after validating the action it's part of
func (h *Holochain) doCommit(a CommittingAction, change *StatusChange) (d *EntryDef, header *Header, entryHash Hash, err error) {

	entryType := a.EntryType()
	entry := a.Entry()
	var l int
	var hash Hash
	l, hash, header, err = h.chain.PrepareHeader(time.Now(), entryType, entry, h.agent.PrivKey(), change)
	if err != nil {
		return
	}
	//TODO	a.header = header
	d, err = h.ValidateAction(a, entryType, nil, []peer.ID{h.nodeID})
	if err != nil {
		if err == ValidationFailedErr {
			err = fmt.Errorf("Invalid entry: %v", entry.Content())
		}
		return
	}
	err = h.chain.addEntry(l, hash, header, entry)
	if err != nil {
		return
	}
	entryHash = header.EntryLink
	return
}

//------------------------------------------------------------
// Commit

type ActionCommit struct {
	entryType string
	entry     Entry
	header    *Header
}

func NewCommitAction(entryType string, entry Entry) *ActionCommit {
	a := ActionCommit{entryType: entryType, entry: entry}
	return &a
}

func (a *ActionCommit) Entry() Entry {
	return a.entry
}

func (a *ActionCommit) EntryType() string {
	return a.entryType
}

func (a *ActionCommit) Name() string {
	return "commit"
}

func (a *ActionCommit) Args() []Arg {
	return []Arg{{Name: "entryType", Type: StringArg}, {Name: "entry", Type: EntryArg}}
}

func (a *ActionCommit) Do(h *Holochain) (response interface{}, err error) {
	var d *EntryDef
	var entryHash Hash
	//	var header *Header
	d, _, entryHash, err = h.doCommit(a, nil)
	if err != nil {
		return
	}
	if d.DataFormat == DataFormatLinks {
		// if this is a Link entry we have to send the DHT Link message
		var le LinksEntry
		entryStr := a.entry.Content().(string)
		err = json.Unmarshal([]byte(entryStr), &le)
		if err != nil {
			return
		}

		bases := make(map[string]bool)
		for _, l := range le.Links {
			_, exists := bases[l.Base]
			if !exists {
				b, _ := NewHash(l.Base)
				h.dht.Send(b, LINK_REQUEST, LinkReq{Base: b, Links: entryHash})
				//TODO errors from the send??
				bases[l.Base] = true
			}
		}
	} else if d.Sharing == Public {
		// otherwise we check to see if it's a public entry and if so send the DHT put message
		_, err = h.dht.Send(entryHash, PUT_REQUEST, PutReq{H: entryHash})
	}
	response = entryHash
	return
}

// sysValidateEntry does system level validation for an entry
// It checks that entry is not nil, and that it conforms to the entry schema in the definition
// and if it's a Links entry that the contents are correctly structured
func sysValidateEntry(h *Holochain, d *EntryDef, entry Entry) (err error) {
	if entry == nil {
		err = errors.New("nil entry invalid")
		return
	}
	// see if there is a schema validator for the entry type and validate it if so
	if d.validator != nil {
		var input interface{}
		if d.DataFormat == DataFormatJSON {
			if err = json.Unmarshal([]byte(entry.Content().(string)), &input); err != nil {
				return
			}
		} else {
			input = entry
		}
		Debugf("Validating %v against schema", input)
		if err = d.validator.Validate(input); err != nil {
			return
		}
	} else if d.DataFormat == DataFormatLinks {
		// Perform base validation on links entries, i.e. that all items exist and are of the right types
		// so first unmarshall the json, and then check that the hashes are real.
		var l struct{ Links []map[string]string }
		err = json.Unmarshal([]byte(entry.Content().(string)), &l)
		if err != nil {
			err = fmt.Errorf("invalid links entry, invalid json: %v", err)
			return
		}
		if len(l.Links) == 0 {
			err = errors.New("invalid links entry: you must specify at least one link")
			return
		}
		for _, link := range l.Links {
			h, ok := link["Base"]
			if !ok {
				err = errors.New("invalid links entry: missing Base")
				return
			}
			if _, err = NewHash(h); err != nil {
				err = fmt.Errorf("invalid links entry: Base %v", err)
				return
			}
			h, ok = link["Link"]
			if !ok {
				err = errors.New("invalid links entry: missing Link")
				return
			}
			if _, err = NewHash(h); err != nil {
				err = fmt.Errorf("invalid links entry: Link %v", err)
				return
			}
			_, ok = link["Tag"]
			if !ok {
				err = errors.New("invalid links entry: missing Tag")
				return
			}
		}

	}
	return
}

func (a *ActionCommit) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	err = sysValidateEntry(h, d, a.entry)
	return
}

func (a *ActionCommit) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	err = NonDHTAction
	return
}

func (a *ActionCommit) CheckValidationRequest(def *EntryDef) (err error) {
	return
}

//------------------------------------------------------------
// Put

type ActionPut struct {
	entryType string
	entry     Entry
	header    *Header
}

func NewPutAction(entryType string, entry Entry, header *Header) *ActionPut {
	a := ActionPut{entryType: entryType, entry: entry, header: header}
	return &a
}

func (a *ActionPut) Name() string {
	return "put"
}

func (a *ActionPut) Args() []Arg {
	return nil
}

func (a *ActionPut) Do(h *Holochain) (response interface{}, err error) {
	err = NonCallableAction
	return
}

func (a *ActionPut) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	err = sysValidateEntry(h, d, a.entry)
	return
}

func RunValidationPhase(h *Holochain, source peer.ID, msgType MsgType, query Hash, handler func(resp ValidateResponse) error) (err error) {
	var r interface{}
	r, err = h.Send(ValidateProtocol, source, msgType, ValidateQuery{H: query})
	if err != nil {
		return
	}
	switch resp := r.(type) {
	case ValidateResponse:
		err = handler(resp)
	default:
		err = fmt.Errorf("expected ValidateResponse from validator got %T", r)
	}
	return
}

func (a *ActionPut) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	//dht.puts <- *m  TODO add back in queueing
	t := msg.Body.(PutReq)
	err = RunValidationPhase(dht.h, msg.From, VALIDATE_PUT_REQUEST, t.H, func(resp ValidateResponse) error {
		a := NewPutAction(resp.Type, &resp.Entry, &resp.Header)
		_, err := dht.h.ValidateAction(a, a.entryType, &resp.Package, []peer.ID{msg.From})

		var status int
		if err != nil {
			dht.dlog.Logf("Put %v rejected: %v", t.H, err)
			status = StatusRejected
		} else {
			status = StatusLive
		}
		entry := resp.Entry
		var b []byte
		b, err = entry.Marshal()
		if err == nil {
			err = dht.put(msg, resp.Type, t.H, msg.From, b, status)
		}
		return err
	})

	response = "queued"
	return
}

func (a *ActionPut) CheckValidationRequest(def *EntryDef) (err error) {
	return
}

//------------------------------------------------------------
// Mod

type ActionMod struct {
	entryType string
	entry     Entry
	header    *Header
	replaces  Hash
}

func NewModAction(entryType string, entry Entry, replaces Hash) *ActionMod {
	a := ActionMod{entryType: entryType, entry: entry, replaces: replaces}
	return &a
}

func (a *ActionMod) Entry() Entry {
	return a.entry
}

func (a *ActionMod) EntryType() string {
	return a.entryType
}

func (a *ActionMod) Name() string {
	return "mod"
}

func (a *ActionMod) Args() []Arg {
	return []Arg{{Name: "entryType", Type: StringArg}, {Name: "entry", Type: EntryArg}, {Name: "replaces", Type: HashArg}}
}

func (a *ActionMod) Do(h *Holochain) (response interface{}, err error) {
	var d *EntryDef
	var entryHash Hash
	d, a.header, entryHash, err = h.doCommit(a, &StatusChange{Action: ModAction, Hash: a.replaces})
	if err != nil {
		return
	}
	if d.Sharing == Public {
		// if it's a public entry send the DHT MOD & PUT messages
		// TODO handle errors better!!
		_, err = h.dht.Send(entryHash, PUT_REQUEST, PutReq{H: entryHash})
		_, err = h.dht.Send(a.replaces, MOD_REQUEST, ModReq{H: a.replaces, N: entryHash})
	}
	response = entryHash
	return
}

func (a *ActionMod) SysValidation(h *Holochain, def *EntryDef, sources []peer.ID) (err error) {
	if def.DataFormat == DataFormatLinks {
		err = errors.New("Can't mod Links entry")
		return
	}
	var header *Header
	header, err = h.chain.GetEntryHeader(a.replaces)
	if err != nil {
		return
	}
	if header.Type != a.entryType {
		err = ErrEntryTypeMismatch
		return
	}
	err = sysValidateEntry(h, def, a.entry)
	return
}

func (a *ActionMod) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	//var hashStatus int
	t := msg.Body.(ModReq)
	from := msg.From
	err = dht.exists(t.H, StatusDefault)
	if err != nil {
		if err == ErrHashNotFound {
			dht.dlog.Logf("don't yet have %s, trying again later", t.H)
			panic("RETRY-MOD NOT IMPLEMENTED")
			// try the del again later
		}
		return
	}
	err = RunValidationPhase(dht.h, msg.From, VALIDATE_MOD_REQUEST, t.N, func(resp ValidateResponse) error {
		a := NewModAction(resp.Type, &resp.Entry, t.H)
		a.header = &resp.Header
		//@TODO what comes back from Validate Del
		_, err = dht.h.ValidateAction(a, resp.Type, &resp.Package, []peer.ID{from})
		if err != nil {
			// how do we record an invalid Mod?
			//@TODO store as REJECTED?
		} else {
			err = dht.mod(msg, t.H, t.N)
		}
		return err
	})
	response = "queued"
	return
}

func (a *ActionMod) CheckValidationRequest(def *EntryDef) (err error) {
	return
}

//------------------------------------------------------------
// Del

type ActionDel struct {
	entryType string
	entry     DelEntry
}

func NewDelAction(entryType string, entry DelEntry) *ActionDel {
	a := ActionDel{entryType: entryType, entry: entry}
	return &a
}

func (a *ActionDel) Name() string {
	return "del"
}

func (a *ActionDel) Entry() Entry {
	var buf []byte
	buf, err := ByteEncoder(a.entry)
	if err != nil {
		panic(err)
	}
	return &GobEntry{C: string(buf)}
}

func (a *ActionDel) EntryType() string {
	return a.entryType
}

func (a *ActionDel) Args() []Arg {
	return []Arg{{Name: "hash", Type: HashArg}, {Name: "message", Type: StringArg}}
}

func (a *ActionDel) Do(h *Holochain) (response interface{}, err error) {
	var d *EntryDef
	var entryHash Hash

	d, _, entryHash, err = h.doCommit(a, &StatusChange{Action: DelAction, Hash: a.entry.Hash})
	if err != nil {
		return
	}

	if d.Sharing == Public {
		// if it's a public entry send the DHT DEL
		_, err = h.dht.Send(a.entry.Hash, DEL_REQUEST, DelReq{H: a.entry.Hash, By: entryHash})
	}
	response = entryHash

	return
}

func (a *ActionDel) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	if d.DataFormat == DataFormatLinks {
		err = errors.New("Can't del Links entry")
		return
	}
	var header *Header
	header, err = h.chain.GetEntryHeader(a.entry.Hash)
	if err != nil {
		return
	}
	if header.Type != a.entryType {
		err = ErrEntryTypeMismatch
		return
	}
	return
}

func (a *ActionDel) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	t := msg.Body.(DelReq)
	from := msg.From
	err = dht.exists(t.H, StatusDefault)
	if err != nil {
		if err == ErrHashNotFound {
			dht.dlog.Logf("don't yet have %s, trying again later", t.H)
			panic("RETRY-DELETE NOT IMPLEMENTED")
			// try the del again later
		}
		return
	}

	err = RunValidationPhase(dht.h, msg.From, VALIDATE_DEL_REQUEST, t.By, func(resp ValidateResponse) error {
		var delEntry DelEntry
		err := ByteDecoder([]byte(resp.Entry.Content().(string)), &delEntry)
		if err != nil {
			return err
		}

		a := NewDelAction(resp.Type, delEntry)
		//@TODO what comes back from Validate Del
		_, err = dht.h.ValidateAction(a, resp.Type, &resp.Package, []peer.ID{from})
		if err != nil {
			// how do we record an invalid DEL?
			//@TODO store as REJECTED
		} else {
			err = dht.del(msg, delEntry.Hash)
		}
		return err
	})
	response = "queued"
	return
}

func (a *ActionDel) CheckValidationRequest(def *EntryDef) (err error) {
	return
}

//------------------------------------------------------------
// Link

type ActionLink struct {
	entryType      string
	links          []Link
	validationBase Hash
}

func NewLinkAction(entryType string, links []Link) *ActionLink {
	a := ActionLink{entryType: entryType, links: links}
	return &a
}

func (a *ActionLink) Name() string {
	return "link"
}

func (a *ActionLink) Args() []Arg {
	return nil
}

func (a *ActionLink) Do(h *Holochain) (response interface{}, err error) {
	err = NonCallableAction
	return
}

func (a *ActionLink) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	//@TODO what sys level links validation?  That they are all valid hash format for the DNA?
	return
}

func (a *ActionLink) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	t := msg.Body.(LinkReq)
	base := t.Base
	from := msg.From
	err = dht.exists(base, StatusLive)
	if err == nil {
		err = dht.exists(t.Base, StatusLive)
		// @TODO what happens if the baseStatus is not StatusLive?
		if err != nil {
			if err == ErrHashNotFound {
				dht.dlog.Logf("don't yet have %s, trying again later", t.Base)
				panic("RETRY-LINK NOT IMPLEMENTED")
				// try the put again later
			}
			return
		}

		err = RunValidationPhase(dht.h, msg.From, VALIDATE_LINK_REQUEST, t.Links, func(resp ValidateResponse) error {
			var le LinksEntry

			if err = json.Unmarshal([]byte(resp.Entry.Content().(string)), &le); err != nil {
				return err
			}

			a := NewLinkAction(resp.Type, le.Links)
			a.validationBase = t.Base
			_, err = dht.h.ValidateAction(a, a.entryType, &resp.Package, []peer.ID{from})
			//@TODO this is "one bad apple spoils the lot" because the app
			// has no way to tell us not to link certain of the links.
			// we need to extend the return value of the app to be able to
			// have it reject a subset of the links.
			if err != nil {
				// how do we record an invalid linking?
				//@TODO store as REJECTED
			} else {
				base := t.Base.String()
				for _, l := range le.Links {
					if base == l.Base {
						if l.LinkAction == DelAction {
							err = dht.delLink(msg, base, l.Link, l.Tag)
						} else {
							err = dht.putLink(msg, base, l.Link, l.Tag)
						}
					}
				}

			}
			return err
		})

		response = "queued"
	} else {
		dht.dlog.Logf("DHTReceive key %v doesn't exist, ignoring", base)
	}
	return
}

func (a *ActionLink) CheckValidationRequest(def *EntryDef) (err error) {
	if def.DataFormat != DataFormatLinks {
		err = errors.New("hash not of a linking entry")
	}
	return
}

//------------------------------------------------------------
// GetLink

type ActionGetLink struct {
	linkQuery *LinkQuery
	options   *GetLinkOptions
}

func NewGetLinkAction(linkQuery *LinkQuery, options *GetLinkOptions) *ActionGetLink {
	a := ActionGetLink{linkQuery: linkQuery, options: options}
	return &a
}

func (a *ActionGetLink) Name() string {
	return "getLink"
}

func (a *ActionGetLink) Args() []Arg {
	return []Arg{{Name: "base", Type: HashArg}, {Name: "tag", Type: StringArg}, {Name: "options", Type: MapArg, MapType: reflect.TypeOf(GetLinkOptions{}), Optional: true}}
}

func (a *ActionGetLink) Do(h *Holochain) (response interface{}, err error) {
	var r interface{}
	r, err = h.dht.Send(a.linkQuery.Base, GETLINK_REQUEST, *a.linkQuery)

	if err == nil {
		switch t := r.(type) {
		case *LinkQueryResp:
			response = t
			if a.options.Load {
				for i := range t.Links {
					var hash Hash
					hash, err = NewHash(t.Links[i].H)
					if err != nil {
						return
					}
					req := GetReq{H: hash, StatusMask: StatusDefault}
					rsp, err := NewGetAction(req, &GetOptions{StatusMask: StatusDefault}).Do(h)
					if err == nil {
						entry := rsp.(GetResp).Entry
						if entry != nil {
							t.Links[i].E = entry.(Entry).Content().(string)
						} else {
							panic(fmt.Sprintf("Nil entry in GetLink.Do response to req: %v", req))
						}

					}
					//TODO better error handling here, i.e break out of the loop and return if error?
				}
			}
		default:
			err = fmt.Errorf("unexpected response type from SendGetLink: %T", t)
		}
	}
	return
}

func (a *ActionGetLink) SysValidation(h *Holochain, d *EntryDef, sources []peer.ID) (err error) {
	//@TODO what sys level getlinks validation?  That they are all valid hash format for the DNA?
	return
}

func (a *ActionGetLink) Receive(dht *DHT, msg *Message) (response interface{}, err error) {
	lq := msg.Body.(LinkQuery)
	var r LinkQueryResp
	r.Links, err = dht.getLink(lq.Base, lq.T, lq.StatusMask)
	response = &r

	return
}
