package server

import (
	"errors"
	"fmt"
	"net/http"
)

type usePageKey struct {
	pkg string

	// ToDo: Generally, this is a pacakge-level identifer and selector identifier.
	// It might be extended to fake identiers for unnamed types later.
	// It should be nerver a local identifer.
	id string
}

// ToDo: for types, also list its values, including locals

func (ds *docServer) identifierUsesPage(w http.ResponseWriter, r *http.Request, pkgPath, identifier string) {
	w.Header().Set("Content-Type", "text/html")

	//log.Println(pkgPath, bareFilename)

	// To avoid some too time-comsuming cases,
	// now only supporting unexported identfiers, which
	// don't need page caching.

	// Add query parameter: scope=a/b/pkg, default is the id containing package.
	// If the id is exported, list the pacakges importing the containing package
	// by use each of them as the scope parameter value.
	// Only search one package for each page show.

	// The search result should be be cached.
	// Use don't care most id uses.
	// Cache the ever searcheds is ok.
	//    map[*ast.Ident][]token.Pos

	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if ds.phase < Phase_Analyzed {
		w.WriteHeader(http.StatusTooEarly)
		ds.loadingPage(w, r)
		return
	}

	// Pages for non-exported identifiers will not be cached.

	useKey := usePageKey{pkg: pkgPath, id: identifier}
	if ds.identifierUsePages[useKey] == nil {
		result, err := ds.buildUsesData(pkgPath, identifier)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "Find uses for (", identifier, ") in ", pkgPath, " error: ", err)
			return
		}
		ds.identifierUsePages[useKey] = ds.buildUsesPage(result)
	}
	w.Write(ds.identifierUsePages[useKey])
}

func (ds *docServer) buildUsesPage(result *UsesResult) []byte {

	return nil
}

type UsesResult struct {
	Identifier string
}

func (ds *docServer) buildUsesData(pkgPath, identifier string) (*UsesResult, error) {
	return nil, errors.New("not implemented yet")
}
