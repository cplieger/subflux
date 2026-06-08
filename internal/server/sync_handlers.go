package server

import (
	"github.com/cplieger/subflux/internal/server/synchandlers"
)

// syncStore is a type alias for the synchandlers.SyncStore interface.
type syncStore = synchandlers.SyncStore

// shiftAndFilterCues delegates to synchandlers.ShiftAndFilterCues.
var shiftAndFilterCues = synchandlers.ShiftAndFilterCues

// findDialogueDenseStart delegates to synchandlers.FindDialogueDenseStart.
var findDialogueDenseStart = synchandlers.FindDialogueDenseStart

// srtToWebVTT delegates to synchandlers.SrtToWebVTT.
var srtToWebVTT = synchandlers.SrtToWebVTT

// msToVTT delegates to synchandlers.MsToVTT.
var msToVTT = synchandlers.MsToVTT
