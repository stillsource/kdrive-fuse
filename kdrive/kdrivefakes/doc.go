// Package kdrivefakes provides in-memory implementations of the kdrive.Files
// and kdrive.Shares interfaces suitable for unit tests.
//
// Each fake supports three call-routing strategies, checked in order:
//
//  1. Stub: a function set on the fake; when non-nil, it handles every call.
//  2. Results: a map keyed by the operation's identifier (file/folder ID);
//     the matching entry provides the return values.
//  3. Default: the fake's zero-value for the return tuple.
//
// All fakes are safe for concurrent use. Every call is recorded on a Calls slice
// for later inspection, so tests can assert "was this called with those args".
package kdrivefakes
