# fastintmap 

[![Build Status](https://travis-ci.org/itsabgr/fastintmap.svg?branch=master)](https://travis-ci.org/itsabgr/fastintmap)
[![GoDoc](https://godoc.org/github.com/itsabgr/fastintmap?status.svg)](https://godoc.org/github.com/itsabgr/fastintmap)
[![Go Report Card](https://goreportcard.com/badge/itsabgr/fastintmap)](https://goreportcard.com/report/github.com/itsabgr/fastintmap)

## Overview

A Golang lock-free thread-safe HashMap optimized for fastest read access.

## Usage

Set a value for a key in the map:

```go
m := &HashMap{}
m.Set(123, "any")
```

Read a value for a key from the map:
```go
amount, ok := m.Get(123)
```

Use the map to count URL requests:
```go
var i int64
actual, _ := m.GetOrInsert(124312, &i)
counter := (actual).(*atomic2.Int64)
counter.Add(1) // increase counter
...
count := counter.Get() // read counter
```

### Benefits over Golangs builtin map

* Faster

* thread-safe access without need of a(n extra) mutex

* [Compare-and-swap](https://en.wikipedia.org/wiki/Compare-and-swap) access for values


### Benefits over [Golangs sync.Map](https://golang.org/pkg/sync/#Map)

* Faster

## Technical details

* Technical design decisions have been made based on benchmarks that are stored in an external repository:
  [go-benchmark](https://github.com/cornelk/go-benchmark)

* The library uses a sorted doubly linked list and a slice as an index into that list.

* It optimizes the slice access by circumventing the Golang size check when reading from the slice.
  Once a slice is allocated, the size of it does not change.
  The library limits the index into the slice, therefore the Golang size check is obsolete.
  When the slice reaches a defined fill rate, a bigger slice is allocated and all keys are recalculated and transferred into the new slice.
