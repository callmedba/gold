package code

import (
	"container/list"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
)

func (d *CodeAnalyzer) AnalyzePackages() {
	log.Println("[analyze packages ...]")

	d.confirmPackageModules()

	// Important for registerFunctionForInvolvedTypeNames and registerValueForItsTypeName.
	d.sortPackagesByDependencies()

	//d.findNearbyPackages()

	//for _, pkg := range d.packageList {
	//	log.Println(">>>>>>>>>>>>>>>>", pkg.DepLevel, pkg.Path())
	//}

	for _, pkg := range d.packageList {
		d.analyzePackage_CollectDeclarations(pkg)
	}

	d.analyzePackage_CollectSomeRuntimeFunctionPositions()

	log.Println("=== recorded type count:", len(d.allTypeInfos))

	log.Println("[analyze packages 2...]")

	for _, pkg := range d.packageList {
		d.analyzePackage_FindTypeSources(pkg)
	}

	log.Println("[analyze packages 4...]")

	d.analyzePackages_CollectSelectors()

	// ToDo: it might be best to not use the NewMethodSet fucntion in std.
	//       Same for NewFieldSet

	log.Println("[analyze packages 4...]")

	d.forbidRegisterTypes = true

	//methedCache := d.analyzePackages_FindImplementations_Old()
	methedCache := d.analyzePackages_FindImplementations()

	d.forbidRegisterTypes = false

	// ...
	//d.analyzePackages_CheckCollectSelectors(methedCache)
	_ = methedCache

	// ToDo: implment new analyzePackages_FindImplementations here
	//       by using the just collected selectors info.

	// ...

	log.Println("[analyze packages done]")
}

func sortPackagesByDepLevels(pkgs []*Package) {
	var seen = make(map[string]struct{}, len(pkgs))
	var calculatePackageDepLevel func(pkg *Package)
	calculatePackageDepLevel = func(pkg *Package) {
		if _, ok := seen[pkg.Path()]; ok {
			return
		}
		seen[pkg.Path()] = struct{}{}

		var max = 0
		for _, dep := range pkg.Deps {
			calculatePackageDepLevel(dep)
			if dep.DepLevel > max {
				max = dep.DepLevel
			} else if dep.DepLevel == 0 {
				log.Println("sortPackagesByDepLevels, calculatePackageDepLevel, the dep.DepLevel is not calculated yet!")
			}
		}
		pkg.DepLevel = max + 1
	}

	for _, pkg := range pkgs {
		calculatePackageDepLevel(pkg)
	}

	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].DepLevel < pkgs[j].DepLevel
	})
}

func (d *CodeAnalyzer) analyzePackages_FindImplementations() (resultMethodCache *typeutil.MethodSetCache) {

	//

	//2. search implementations
	//a. 1st pass: collect all interface method signatures. Each interface method signature maintains a type list.
	//   map[sigID][]*type
	//b. 2nd pass: for each method of every type, if it is an interface method, register the type to the interface method
	//c. 3rd pass: for each interface type, iterate its method, increase Type.counter (must be handled in a single thread)
	//d. sort the implementations by package distances to the interface type. The shorter common prefixm the longer two packages distance.

	// step 1: register all method signatures of underlying interface types.
	//         create a type list for each signature.
	// step 2: iteration all types, calculate their method signatures,
	//         (interface types can use their underlying cache calculated in step 1)
	//         ignore signatures which are note recorded in step 1.
	//         register the type into the type lists of method signatures.
	// step 3: iterate all underlying interfaces, iterate all method signatures,
	//         iterate the type list of a signature, TypeInfo.counter++
	//         ...

	type UnderlyingInterfaceInfo struct {
		t             *TypeInfo
		methodIndexes []uint32
		underlieds    []*TypeInfo // including the underlying itself
	}

	// ToDo: use map[InterfaceTypeIndex]*TypeInfo?
	var interfaceUnderlyings typeutil.Map

	//var interfaceUnderlyingTypes = make([]*TypeInfo, 0, 1024)
	//for _, t := range d.allTypeInfos {
	// New types might be registered in this loop,
	// so traditional for-loop is used here.
	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]

		// ToDo: auto register underlying type in RegisterType.
		underlying := t.TT.Underlying()
		underlyingTypeInfo := d.RegisterType(underlying) // underlying must have been already registered
		t.Underlying = underlyingTypeInfo
		underlyingTypeInfo.Underlying = underlyingTypeInfo

		if i, ok := underlying.(*types.Interface); ok && i.NumMethods() > 0 {
			var uiInfo *UnderlyingInterfaceInfo
			info := interfaceUnderlyings.At(i)
			if interfaceUnderlyings.At(i) == nil {
				//interfaceUnderlyingTypes = append(interfaceUnderlyingTypes, underlyingTypeInfo)
				uiInfo = &UnderlyingInterfaceInfo{t: underlyingTypeInfo, underlieds: make([]*TypeInfo, 0, 3)}
				interfaceUnderlyings.Set(i, uiInfo)
				//log.Printf("!!! %T\n", uiInfo.t.TT)
			} else {
				uiInfo, _ = info.(*UnderlyingInterfaceInfo)
			}
			uiInfo.underlieds = append(uiInfo.underlieds, t)
		}
	}

	log.Println("number of underlying interfaces:", interfaceUnderlyings.Len())
	//interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
	//	uiInfo := info.(*UnderlyingInterfaceInfo)
	//	log.Println("     ", uiInfo.t.TT)
	//	for _, t := range uiInfo.underlieds {
	//		log.Println("           ", t.TT)
	//	}
	//})

	var lastMethodIndex uint32
	var allMethods = make(map[MethodSignature]uint32, 8196)
	var method2TypeIndexes = make([][]uint32, 0, 8196)
	var cache typeutil.MethodSetCache
	resultMethodCache = &cache

	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)
		//log.Printf("### %d %T\n", uiInfo.t.index, uiInfo.t.TT)
		//methodSet := cache.MethodSet(uiInfo.t.TT)
		//uiInfo.methodIndexes = make([]uint32, methodSet.Len())
		selectors := uiInfo.t.AllMethods
		uiInfo.methodIndexes = make([]uint32, len(selectors))

		//for i := methodSet.Len() - 1; i >= 0; i-- {
		for i := len(selectors) - 1; i >= 0; i-- {
			x := d.lastTypeIndex

			//sel := methodSet.At(i)
			//funcObj, ok := sel.Obj().(*types.Func)
			//if !ok {
			//	panic("not a types.Func")
			//}
			//
			//sig := d.BuildMethodSignatureFromFuncObject(funcObj) // will not produce new type registrations for sure
			sel := selectors[i]
			funcSig, ok := sel.Method.Type.TT.(*types.Signature)
			if !ok {
				panic(fmt.Sprintf("not a types.Signature: %T", sel.Method.Type.TT))
			}
			pkgImportPath := ""
			if sel.Method.Pkg != nil {
				pkgImportPath = sel.Method.Pkg.Path()
			}

			sig := d.BuildMethodSignatureFromFunctionSignature(funcSig, sel.Method.Name, pkgImportPath)

			if d.lastTypeIndex > x {
				log.Println("       > ", uiInfo.t.TT)
				log.Println("             >> ", sel)
			}

			methodIndex, ok := allMethods[sig]
			if ok {
				method2TypeIndexes[methodIndex] = append(method2TypeIndexes[methodIndex], uiInfo.t.index)
			} else {
				methodIndex = lastMethodIndex
				lastMethodIndex++
				allMethods[sig] = methodIndex

				typeIndexes := make([]uint32, 0, 8)
				typeIndexes = append(typeIndexes, uiInfo.t.index)

				//log.Printf("   $$$ %d %T\n", uiInfo.t.index, d.allTypeInfos[uiInfo.t.index].TT)

				// method2TypeIndexes[methodIndex] = typeIndexes
				method2TypeIndexes = append(method2TypeIndexes, typeIndexes)
			}
			uiInfo.methodIndexes[i] = methodIndex

			//if len(selectors) == 1 {
			//	if sel.Name() == "Error" {
			//		log.Println("#################### uiInfo.t: ", uiInfo.t)
			//		log.Printf("=== methodIndex: %d %x %x",
			//			methodIndex,
			//			d.RegisterType(d.builtinPkg.PPkg.Types.Scope().Lookup("string").(*types.TypeName).Type()).index,
			//			d.RegisterType(types.Universe.Lookup("string").(*types.TypeName).Type()).index,
			//		)
			//		log.Printf("=== sig: %#v", sig)
			//	}
			//}
		}
	})

	//log.Println("number of method signatures:", lastMethodIndex, len(allMethods), len(method2TypeIndexes))
	//for methodIndex, typeIndexes := range method2TypeIndexes {
	//	log.Println("     method#", methodIndex)
	//	for _, typeIndex := range typeIndexes {
	//		t := d.allTypeInfos[typeIndex]
	//		log.Printf("          %v : %T", t.TT, t.TT)
	//	}
	//}

	// log.Println("method2TypeIndexes = \n", method2TypeIndexes)

	for _, t := range d.allTypeInfos {
		//log.Println("111>>>", t.TT)
		if _, ok := t.TT.Underlying().(*types.Interface); ok {
			continue
		}

		//methodSet := cache.MethodSet(t.TT)
		selectors := t.AllMethods
		//log.Println("222>>>", t.TT, methodSet.Len())
		//for i := methodSet.Len() - 1; i >= 0; i-- {
		for i := len(selectors) - 1; i >= 0; i-- {
			//sel := methodSet.At(i)
			//funcObj, ok := sel.Obj().(*types.Func)
			//if !ok {
			//	panic("not a types.Func")
			//}
			//
			//sig := d.BuildMethodSignatureFromFuncObject(funcObj) // will not produce new type registrations for sure
			sel := selectors[i]
			funcSig, ok := sel.Method.Type.TT.(*types.Signature)
			if !ok {
				panic("not a types.Signature")
			}
			pkgImportPath := ""
			if sel.Method.Pkg != nil {
				pkgImportPath = sel.Method.Pkg.Path()
			}

			sig := d.BuildMethodSignatureFromFunctionSignature(funcSig, sel.Method.Name, pkgImportPath)
			methodIndex, ok := allMethods[sig]
			//log.Println("333>>>", methodIndex, ok)
			if ok {
				method2TypeIndexes[methodIndex] = append(method2TypeIndexes[methodIndex], t.index)
			}

			//if len(selectors) == 1 {
			//	if sel.Name() == "Error" {
			//		log.Println("!!!!!!!!!!!!!!! t: ", t)
			//		log.Printf("=== methodIndex: %d %x %x",
			//			methodIndex,
			//			d.RegisterType(d.builtinPkg.PPkg.Types.Scope().Lookup("string").(*types.TypeName).Type()).index,
			//			d.RegisterType(types.Universe.Lookup("string").(*types.TypeName).Type()).index,
			//		)
			//		log.Printf("=== sig: %#v", sig)
			//	}
			//}
		}
	}

	log.Println("number of interface method signatures:", lastMethodIndex, len(allMethods), len(method2TypeIndexes))
	//for methodIndex, typeIndexes := range method2TypeIndexes {
	//	log.Println("     method#", methodIndex)
	//	for _, typeIndex := range typeIndexes {
	//		t := d.allTypeInfos[typeIndex]
	//		log.Println("          ", t.TT)
	//	}
	//}

	typeLookupTable := d.tempTypeLookupTable()
	defer d.resetTempTypeLookupTable()

	var searchRound uint32 = 0
	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)

		typeIndexes := method2TypeIndexes[uiInfo.methodIndexes[0]]
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			t.counter = searchRound + 1
		}
		searchRound++

		//if len(uiInfo.methodIndexes) == 1 {
		//	sel := uiInfo.t.AllMethods[0]
		//	if sel.Name() == "Error" {
		//		log.Println("======================================= uiInfo.t: ", uiInfo.t)
		//		log.Println("=== typeIndexes:", typeIndexes)
		//	}
		//}

		for _, methodIndex := range uiInfo.methodIndexes[1:] {
			typeIndexes = method2TypeIndexes[methodIndex]
			for _, typeIndex := range typeIndexes {
				t := d.allTypeInfos[typeIndex]
				if t.counter == searchRound {
					t.counter = searchRound + 1
				}
			}
			searchRound++
		}

		count := 0
		//typeIndexes = method2TypeIndexes[uiInfo.methodIndexes[len(uiInfo.methodIndexes)-1]]
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				////t.Implements = append(t.Implements, uiInfo.t)
				//t.Implements = append(t.Implements, uiInfo.underlieds...)
				for _, it := range uiInfo.underlieds {
					t.Implements = append(t.Implements, Implementation{Impler: t, Interface: it})
				}
				count++
			}
		}

		// Register non-pointer ones firstly, then
		// register pointer ones whose bases have not been registered.
		d.resetTempTypeLookupTable()
		impBy := make([]*TypeInfo, 0, count)
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				if _, ok := t.TT.(*types.Pointer); !ok {
					if itt, ok := t.TT.Underlying().(*types.Interface); ok {
						ittInfo := interfaceUnderlyings.At(itt).(*UnderlyingInterfaceInfo)
						for _, it := range ittInfo.underlieds {
							impBy = append(impBy, it)
							typeLookupTable[it.index] = struct{}{}
						}
					} else {
						impBy = append(impBy, t)
						typeLookupTable[typeIndex] = struct{}{}
					}
				}
			}
		}
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				if ptt, ok := t.TT.(*types.Pointer); ok {
					bt := d.RegisterType(ptt.Elem()) // 333 a: here to check why new types are registered
					if _, reged := typeLookupTable[bt.index]; !reged {
						impBy = append(impBy, t)
					}
				}
			}
		}
		uiInfo.t.ImplementedBys = impBy

		//log.Println("111 @@@", uiInfo.t.TT, ", uiInfo.methodIndexes:", uiInfo.methodIndexes)
		//for _, impBy := range impBy {
		//	log.Println("     ", impBy.TT)
		//}
	})

	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)
		for _, t := range uiInfo.underlieds {
			t.Implements = uiInfo.t.Implements
			t.ImplementedBys = uiInfo.t.ImplementedBys
		}
	})

	//for _, t := range d.allTypeInfos {
	//	if len(t.Implements) > 0 {
	//		log.Println(t.TT, "implements:")
	//		for _, it := range t.Implements {
	//			log.Println("     ", it.TT)
	//		}
	//	}
	//}

	for _, t := range d.allTypeInfos {
		if len(t.Implements) == 0 {
			continue
		}

		if ptt, ok := t.TT.(*types.Pointer); ok {
			bt := d.RegisterType(ptt.Elem()) // 333 b: here to check why new types are registered
			//bt.StarImplements = t.Implements

			// merge non-pointer and pointer implements.
			d.resetTempTypeLookupTable()
			for _, impl := range bt.Implements {
				typeLookupTable[impl.Interface.index] = struct{}{}
			}
			for _, impl := range t.Implements {
				if _, ok := typeLookupTable[impl.Interface.index]; ok {
					continue
				}
				//impl := impl // not needed, for the .Implements slice element is not pointer.
				bt.Implements = append(bt.Implements, impl)
			}
			t.Implements = nil

			// remove unnamed interfaces whose have named underlieds.
			// ToDo: avoid removing aliases to unnamed ones.
			// (The work is moved to package datail page generation.)
		}
	}

	return
}

// ToDo:
// The current implementaiton-finding algorithm uses TypeInfo.index as judge conidition.
// So the implementation is not ok for concurrency safe. To make it concurrentcy safe,
// we can sort each method2TypeIndexes slices, and copy the one for the first method,
// then get the overlapping for consequencing method slices.
// However, it looks the current implementation is fast enough.

func (d *CodeAnalyzer) analyzePackages_FindImplementations_Old() (resultMethodCache *typeutil.MethodSetCache) {
	//

	//2. search implementations
	//a. 1st pass: collect all interface method signatures. Each interface method signature maintains a type list.
	//   map[sigID][]*type
	//b. 2nd pass: for each method of every type, if it is an interface method, register the type to the interface method
	//c. 3rd pass: for each interface type, iterate its method, increase Type.counter (must be handled in a single thread)
	//d. sort the implementations by package distances to the interface type. The shorter common prefixm the longer two packages distance.

	// step 1: register all method signatures of underlying interface types.
	//         create a type list for each signature.
	// step 2: iteration all types, calculate their method signatures,
	//         (interface types can use their underlying cache calculated in step 1)
	//         ignore signatures which are note recorded in step 1.
	//         register the type into the type lists of method signatures.
	// step 3: iterate all underlying interfaces, iterate all method signatures,
	//         iterate the type list of a signature, TypeInfo.counter++
	//         ...

	type UnderlyingInterfaceInfo struct {
		t             *TypeInfo
		methodIndexes []uint32
		underlieds    []*TypeInfo // including the underlying itself
	}

	// ToDo: use map[InterfaceTypeIndex]*TypeInfo?
	var interfaceUnderlyings typeutil.Map

	//var interfaceUnderlyingTypes = make([]*TypeInfo, 0, 1024)
	//for _, t := range d.allTypeInfos {
	// New types might be registered in this loop,
	// so traditional for-loop is used here.
	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]

		// ToDo: auto register underlying type in RegisterType.
		underlying := t.TT.Underlying()
		underlyingTypeInfo := d.RegisterType(underlying) // underlying must have been already registered
		t.Underlying = underlyingTypeInfo
		underlyingTypeInfo.Underlying = underlyingTypeInfo

		if i, ok := underlying.(*types.Interface); ok && i.NumMethods() > 0 {
			var uiInfo *UnderlyingInterfaceInfo
			info := interfaceUnderlyings.At(i)
			if interfaceUnderlyings.At(i) == nil {
				//interfaceUnderlyingTypes = append(interfaceUnderlyingTypes, underlyingTypeInfo)
				uiInfo = &UnderlyingInterfaceInfo{t: underlyingTypeInfo, underlieds: make([]*TypeInfo, 0, 3)}
				interfaceUnderlyings.Set(i, uiInfo)
				//log.Printf("!!! %T\n", uiInfo.t.TT)
			} else {
				uiInfo, _ = info.(*UnderlyingInterfaceInfo)
			}
			uiInfo.underlieds = append(uiInfo.underlieds, t)
		}
	}

	log.Println("number of underlying interfaces:", interfaceUnderlyings.Len())
	//interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
	//	uiInfo := info.(*UnderlyingInterfaceInfo)
	//	log.Println("     ", uiInfo.t.TT)
	//	for _, t := range uiInfo.underlieds {
	//		log.Println("           ", t.TT)
	//	}
	//})

	var lastMethodIndex uint32
	var allMethods = make(map[MethodSignature]uint32, 8196)
	var method2TypeIndexes = make([][]uint32, 0, 8196)
	var cache typeutil.MethodSetCache
	resultMethodCache = &cache

	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)
		//log.Printf("### %d %T\n", uiInfo.t.index, uiInfo.t.TT)
		methodSet := cache.MethodSet(uiInfo.t.TT)
		uiInfo.methodIndexes = make([]uint32, methodSet.Len())

		for i := methodSet.Len() - 1; i >= 0; i-- {
			sel := methodSet.At(i)
			funcObj, ok := sel.Obj().(*types.Func)
			if !ok {
				panic("not a types.Func")
			}
			x := d.lastTypeIndex
			sig := d.BuildMethodSignatureFromFuncObject(funcObj) // will not produce new type registrations for sure
			if d.lastTypeIndex > x {
				log.Println("       > ", uiInfo.t.TT)
				log.Println("             >> ", sel)
			}

			methodIndex, ok := allMethods[sig]
			if ok {
				method2TypeIndexes[methodIndex] = append(method2TypeIndexes[methodIndex], uiInfo.t.index)
			} else {
				methodIndex = lastMethodIndex
				lastMethodIndex++
				allMethods[sig] = methodIndex

				typeIndexes := make([]uint32, 0, 8)
				typeIndexes = append(typeIndexes, uiInfo.t.index)

				//log.Printf("   $$$ %d %T\n", uiInfo.t.index, d.allTypeInfos[uiInfo.t.index].TT)

				// method2TypeIndexes[methodIndex] = typeIndexes
				method2TypeIndexes = append(method2TypeIndexes, typeIndexes)
			}
			uiInfo.methodIndexes[i] = methodIndex
		}
	})

	//log.Println("number of method signatures:", lastMethodIndex, len(allMethods), len(method2TypeIndexes))
	//for methodIndex, typeIndexes := range method2TypeIndexes {
	//	log.Println("     method#", methodIndex)
	//	for _, typeIndex := range typeIndexes {
	//		t := d.allTypeInfos[typeIndex]
	//		log.Printf("          %v : %T", t.TT, t.TT)
	//	}
	//}

	// log.Println("method2TypeIndexes = \n", method2TypeIndexes)

	for _, t := range d.allTypeInfos {
		//log.Println("111>>>", t.TT)
		if _, ok := t.TT.Underlying().(*types.Interface); ok {
			continue
		}

		methodSet := cache.MethodSet(t.TT)
		//log.Println("222>>>", t.TT, methodSet.Len())
		for i := methodSet.Len() - 1; i >= 0; i-- {
			sel := methodSet.At(i)
			funcObj, ok := sel.Obj().(*types.Func)
			if !ok {
				panic("not a types.Func")
			}

			sig := d.BuildMethodSignatureFromFuncObject(funcObj) // will not produce new type registrations for sure

			methodIndex, ok := allMethods[sig]
			//log.Println("333>>>", methodIndex, ok)
			if ok {
				method2TypeIndexes[methodIndex] = append(method2TypeIndexes[methodIndex], t.index)
			}
		}
	}

	log.Println("number of interface method signatures:", lastMethodIndex, len(allMethods), len(method2TypeIndexes))
	//for methodIndex, typeIndexes := range method2TypeIndexes {
	//	log.Println("     method#", methodIndex)
	//	for _, typeIndex := range typeIndexes {
	//		t := d.allTypeInfos[typeIndex]
	//		log.Println("          ", t.TT)
	//	}
	//}

	typeLookupTable := d.tempTypeLookupTable()
	defer d.resetTempTypeLookupTable()

	var searchRound uint32 = 0
	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)

		typeIndexes := method2TypeIndexes[uiInfo.methodIndexes[0]]
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			t.counter = searchRound + 1
		}
		searchRound++

		if len(uiInfo.methodIndexes) == 1 {
			sel := uiInfo.t.AllMethods[0]
			if sel.Name() == "Error" {
				log.Println("======================================= uiInfo.t: ", uiInfo.t)
				log.Println("=== typeIndexes:", typeIndexes)
			}
		}

		for _, methodIndex := range uiInfo.methodIndexes[1:] {
			typeIndexes = method2TypeIndexes[methodIndex]
			for _, typeIndex := range typeIndexes {
				t := d.allTypeInfos[typeIndex]
				if t.counter == searchRound {
					t.counter = searchRound + 1
				}
			}
			searchRound++
		}

		count := 0
		//typeIndexes = method2TypeIndexes[uiInfo.methodIndexes[len(uiInfo.methodIndexes)-1]]
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				////t.Implements = append(t.Implements, uiInfo.t)
				//t.Implements = append(t.Implements, uiInfo.underlieds...)
				for _, it := range uiInfo.underlieds {
					t.Implements = append(t.Implements, Implementation{Impler: t, Interface: it})
				}
				count++
			}
		}

		// Register non-pointer ones firstly, then
		// register pointer ones whose bases have not been registered.
		d.resetTempTypeLookupTable()
		impBy := make([]*TypeInfo, 0, count)
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				if _, ok := t.TT.(*types.Pointer); !ok {
					if itt, ok := t.TT.Underlying().(*types.Interface); ok {
						ittInfo := interfaceUnderlyings.At(itt).(*UnderlyingInterfaceInfo)
						for _, it := range ittInfo.underlieds {
							impBy = append(impBy, it)
							typeLookupTable[it.index] = struct{}{}
						}
					} else {
						impBy = append(impBy, t)
						typeLookupTable[typeIndex] = struct{}{}
					}
				}
			}
		}
		for _, typeIndex := range typeIndexes {
			t := d.allTypeInfos[typeIndex]
			if t.counter == searchRound {
				if ptt, ok := t.TT.(*types.Pointer); ok {
					bt := d.RegisterType(ptt.Elem()) // 333 a: here to check why new types are registered
					if _, reged := typeLookupTable[bt.index]; !reged {
						impBy = append(impBy, t)
					}
				}
			}
		}
		uiInfo.t.ImplementedBys = impBy

		//log.Println("111 @@@", uiInfo.t.TT, ", uiInfo.methodIndexes:", uiInfo.methodIndexes)
		//for _, impBy := range impBy {
		//	log.Println("     ", impBy.TT)
		//}
	})

	interfaceUnderlyings.Iterate(func(_ types.Type, info interface{}) {
		uiInfo := info.(*UnderlyingInterfaceInfo)
		for _, t := range uiInfo.underlieds {
			t.Implements = uiInfo.t.Implements
			t.ImplementedBys = uiInfo.t.ImplementedBys
		}
	})

	//for _, t := range d.allTypeInfos {
	//	if len(t.Implements) > 0 {
	//		log.Println(t.TT, "implements:")
	//		for _, it := range t.Implements {
	//			log.Println("     ", it.TT)
	//		}
	//	}
	//}

	for _, t := range d.allTypeInfos {
		if len(t.Implements) == 0 {
			continue
		}

		if ptt, ok := t.TT.(*types.Pointer); ok {
			bt := d.RegisterType(ptt.Elem()) // 333 b: here to check why new types are registered
			//bt.StarImplements = t.Implements

			// merge non-pointer and pointer implements.
			d.resetTempTypeLookupTable()
			for _, impl := range bt.Implements {
				typeLookupTable[impl.Interface.index] = struct{}{}
			}
			for _, impl := range t.Implements {
				if _, ok := typeLookupTable[impl.Interface.index]; ok {
					continue
				}
				//impl := impl // not needed, for the .Implements slice element is not pointer.
				bt.Implements = append(bt.Implements, impl)
			}
			t.Implements = nil

			// remove unnamed interfaces whose have named underlieds.
			// ToDo: avoid removing aliases to unnamed ones.
			// (The work is moved to package datail page generation.)
		}
	}

	return
}

func (d *CodeAnalyzer) analyzePackages_CollectSelectors() {
	log.Println("=== analyze struct promoted fields/methods ...")

	// The method set returned by types.NewMethodSet loses much info.
	// So a custom implementation is needed.

	//var printSelectors = func(t *TypeInfo) {
	//	if t.DirectSelectors != nil {
	//		for i, sel := range t.DirectSelectors {
	//			log.Println(i, ">", sel.Id)
	//		}
	//	}
	//}

	var selectorMaps []map[string]*Selector

	var smm = &SeleterMapManager{
		apply: func() (r map[string]*Selector) {
			if selectorMaps == nil {
				selectorMaps = make([]map[string]*Selector, 8, 32)
				for i := range selectorMaps {
					selectorMaps[i] = make(map[string]*Selector, 128)
				}
			}
			if n := len(selectorMaps); n > 0 {
				r = selectorMaps[n-1]
				selectorMaps = selectorMaps[:n-1]
				return
			}
			log.Println("more than", len(selectorMaps), "being used now.")
			r = make(map[string]*Selector, 128)
			return
		},
		release: func(r map[string]*Selector) {
			for k := range r {
				delete(r, k)
			}

			if selectorMaps == nil {
				//return // should not
				panic("should not")
			}

			if len(selectorMaps) >= cap(selectorMaps) {
				log.Println("more than", cap(selectorMaps), "in free now.")
				return
			}

			selectorMaps = append(selectorMaps, r)
		},
	}

	var currentCounter uint32

	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]
		t.counter = 0
	}

	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]

		currentCounter++ // faster than map
		//log.Println("===================================", currentCounter)
		d.collectSelectorsForInterfaceType(t, 0, currentCounter, smm)
	}

	var checkedTypes = make(map[uint32]uint16) // type index: embedding depth
	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]

		//currentCounter++ // can't replace map

		d.collectSelectorsFroNonInterfaceType(t, smm, checkedTypes)

		// print selectors
		//if len(t.AllMethods)+len(t.AllFields) > 0 {
		//	log.Println("============== t=", t)
		//}
		//if len(t.AllMethods) > 0 {
		//	PrintSelectors("methods", t.AllMethods)
		//}
		//if len(t.AllFields) > 0 {
		//	PrintSelectors("fields", t.AllFields)
		//}
	}

	// ToDo: verify the methodsets are the same as typeutl.MethodSet

	//var interfaceUnderlyingTypes = make([]*TypeInfo, 0, 1024)
	//for _, t := range d.allTypeInfos {
	// New types might be registered in this loop,
	// so traditional for-loop is used here.
	//for i := 0; i < len(d.allTypeInfos); i++ {
	//	t := d.allTypeInfos[i]
	//	underlying := t.TT.Underlying()
	//	switch
	//	if i, ok := underlying.(*types.Interface); ok && i.NumMethods() > 0 {
	//	}
	//}

	//log.Println(types.Unsafe.Scope().Lookup("Pointer").Type().Underlying())
	//log.Printf("%v", types.Unsafe.Scope().Lookup("Sizeof").Type())

	//log.Printf("%v", d.packageTable["builtin"])
	//log.Printf("%v", d.packageTable["builtin"].PPkg)
	//log.Printf("%v", d.packageTable["builtin"].PPkg.Types)
	//log.Printf("%v", d.packageTable["builtin"].PPkg.Types.Scope())
	//log.Printf("%v", d.packageTable["builtin"].PPkg.Types.Scope().Lookup("len"))
	//log.Printf("%v", d.packageTable["builtin"].PPkg.Types.Scope().Lookup("print").Type())

	// ToDo: iterate all types, and register some of them in respective pacakges.
	// * exported declared type aliases and named types
	// * non-exported declared type aliases and named types
	// * unnamed types which are types of exported variables/fields
	// For all these types,
	// * record which exported functions use them.
	// * unnamed pointer types will be ignored, their method set
	//   recoreded in their respective base types.

	// When generate docs:
	// * unnamed chan/array/map/slice/func/pointer types are not important.
	// * some unnamed interface and struct types are important.
	//

	// The ssame unnamed typed might appear in several different declarations.
	// The docs for declarations might be different.
}

// ToDo: also check method signatures.
func (d *CodeAnalyzer) analyzePackages_CheckCollectSelectors(cache *typeutil.MethodSetCache) {
	for i := 0; i < len(d.allTypeInfos); i++ {
		t := d.allTypeInfos[i]
		switch tt := t.TT.(type) {
		case *types.Interface:
			if cache.MethodSet(tt).Len() != len(t.AllMethods) {
				panic(fmt.Sprintf("%v: interface (%d) method numbers not match. %d : %d.\n %v", t, t.index, cache.MethodSet(t.TT).Len(), len(t.AllMethods), t.AllMethods))
			}
		case *types.Pointer:
			switch btt := tt.Elem(); btt.Underlying().(type) {
			case *types.Interface, *types.Pointer:
				if num := cache.MethodSet(tt).Len(); num != 0 || len(t.AllMethods) != 0 {
					panic(fmt.Sprintf("%v: should not have methods. %d : %d", t, num, len(t.AllMethods)))
				}
			default:
				ttset := cache.MethodSet(tt)
				bttset := cache.MethodSet(btt)

				typesCount := len(d.allTypeInfos)
				bt := d.RegisterType(btt)
				num1, num2 := 0, 0
				for _, sel := range bt.AllMethods {
					num2++
					if !sel.PointerReceiverOnly() {
						num1++
					}
				}

				if len(d.allTypeInfos) > typesCount {
					//log.Println("> new types are added:", btt)
				} else if ttset.Len() < num2 || bttset.Len() < num1 {
					//} else if ttset.Len() != num2 || bttset.Len() != num1 {
					// This is a bug in typeutil when computing methodset:

					log.Println("      promoted selectors collected?", t.attributes|promotedSelectorsCollected != 0, bt.attributes|promotedSelectorsCollected != 0)
					log.Println("      >>", bttset)
					panic(fmt.Sprintf("%v: method numbers not match: %d : %d and %d : %d. (%d) %v : %v", t, ttset.Len(), num2, bttset.Len(), num1, len(bt.DirectSelectors), bt.DirectSelectors, bt.AllMethods))
				}
			}
		}
	}
}

// ...

type SelectListManager struct {
	current *list.List
	free    *list.List
}

func NewSelectListManager() *SelectListManager {
	return &SelectListManager{
		current: list.New(),
		free:    list.New(),
	}
}

type SeleterMapManager struct {
	apply   func() (r map[string]*Selector)
	release func(r map[string]*Selector)
}

//var debug = false

func (d *CodeAnalyzer) collectSelectorsForInterfaceType(t *TypeInfo, depth int, currentCounter uint32, smm *SeleterMapManager) (r bool) {

	//if !debug {
	//	debug = t.TypeName != nil && t.TypeName.Name() == "Hasher111"
	//	if debug {
	//		defer func() {
	//			debug = false
	//		}()
	//	}
	//}

	//if debug {
	//	log.Println(">>> ", t, ", depth:", depth, ", counters:", t.counter, currentCounter, ", promoted:", (t.attributes&promotedSelectorsCollected) != 0)
	//}

	if t.counter == currentCounter {
		//panic(fmt.Sprintf("recursive interface embedding. %d, %s", t.counter, t.TT))
		r = true
		return
	}

	t.counter = currentCounter
	//log.Println(">>> ", t.counter, t.TT)

	if (t.attributes & promotedSelectorsCollected) != 0 {
		return
	}

	// ToDo: maintain an interface type list in the outer loop to avoid the assertion.
	itt, ok := t.TT.Underlying().(*types.Interface)
	if !ok {
		return
	}

	if t.Underlying == nil {
		// already set interface.Underlying in RegisterType now
		panic(fmt.Sprint("should never happen:", t.TT))

		// ToDo: move to RegisterType.
		underlying := t.TT.Underlying()
		UnderlyingTypeInfo := d.RegisterType(underlying)
		t.Underlying = UnderlyingTypeInfo
		UnderlyingTypeInfo.Underlying = UnderlyingTypeInfo
	}

	t.attributes |= promotedSelectorsCollected

	// ToDo: field and parameter/result interface types don't satisfy this.
	//if (t.Underlying.attributes & directSelectorsCollected) == 0 {
	//	panic("unnamed interface should have collected direct selectors now. " + fmt.Sprintf("%#v", t))
	//}

	//if debug {
	//	log.Println("==== 111", depth, t)
	//}
	if t != t.Underlying {
		//if t.TypeName.Name() == "TokenReviewInterface" || t.TypeName.Name() == "TokenReviewExpansion" {
		//	debug = true
		//	defer func() {
		//		debug = false
		//	}()
		//}
		// t is a named interface type.

		//if debug {
		//	log.Println("xxx ===", t.TT, len(t.DirectSelectors), "\n",
		//		t.Underlying.TT, len(t.Underlying.DirectSelectors), "\n",
		//		t.Underlying.DirectSelectors)
		//}

		//log.Println("222", depth)
		if (t.Underlying.attributes & directSelectorsCollected) == 0 {
			panic("unnamed interface should have collected direct selectors now. " +
				fmt.Sprintf("underlying index: %v. index: %v. name: %#v. %#v. %v",
					t.Underlying.index, t.index, t.TypeName.Name(), t.Underlying.TT, t.TT.Underlying()))
		}
		//if t.DirectSelectors != nil {
		//	panic("Selectors of named interface should be blank now")
		//}
		d.collectSelectorsForInterfaceType(t.Underlying, depth, currentCounter, smm)

		t.DirectSelectors = t.Underlying.DirectSelectors
		t.AllMethods = t.Underlying.AllMethods

		//if debug {
		//	log.Println("yyy ===", t.TT, len(t.AllMethods), "\n",
		//		t.Underlying.TT, len(t.Underlying.AllMethods), "\n",
		//		t.Underlying.AllMethods)
		//}
	} else { // t == t.Underlying
		//if debug {
		//	log.Println("333", depth)
		//}
		if (t.Underlying.attributes & directSelectorsCollected) == 0 {
			//if depth == 0 {
			//	return // ToDo: temp ignore field and paramter/result unnamed interface types
			//}
			log.Println("!!! t.index:", t.index, t.TT)
			panic("unnamed interface should have collected direct selectors now. " + fmt.Sprintf("%#v", t))
		}

		hasEmbeddings := false
		for _, s := range t.DirectSelectors {
			if s.Field != nil {
				hasEmbeddings = true
				break
			}
		}

		//if n := itt.NumEmbeddeds(); n == 0 {
		if !hasEmbeddings { // the embedding ones might overlap with non-embedding ones
			//if debug {
			//	log.Println("444", depth)
			//}
			t.AllMethods = t.DirectSelectors
		} else {
			//if debug {
			//	log.Println("555", depth)
			//}
			selectors := smm.apply()
			defer func() {
				smm.release(selectors)
			}()

			t.AllMethods = make([]*Selector, 0, len(t.DirectSelectors)+2*itt.NumEmbeddeds())

			for _, sel := range t.DirectSelectors {
				if sel.Method != nil {
					if old, ok := selectors[sel.Id]; ok {
						if old.Method.Type != sel.Method.Type {
							panic("direct overlapped interface methods and signatures are different")
						} else {
							//log.Println("$$$ overlapping interface method:", sel.Id, ". (allowed since Go 1.14)")
							//log.Println("            ", t.TT)
							//log.Println("            ", t.DirectSelectors)

							// ToDo: go-ethethum has 3 such cases? why?
							//panic("direct overlapped interface methods are not allowed")
						}
					} else {
						selectors[sel.Id] = sel
						t.AllMethods = append(t.AllMethods, sel)
					}
				} else { // sel.Field != nil

					// It is some quirk here. An unnamed interface type
					// interface {I} might be the underlying type of
					// named interface type I.

					//log.Println("      ", sel.Field.Type)
					//d.collectSelectorsForInterfaceType(sel.Field.Type, depth+1, currentCounter, smm)
					//for _, sel := range sel.Field.Type.AllMethods {
					//	if old, ok := selectors[sel.Id]; ok {
					//		if old.Method.Type != sel.Method.Type {
					//			panic("overlapped interface methods but signatures are different")
					//		} else {
					//			log.Println("overlapping interface method:", sel.Id, ". (allowed since Go 1.14)")
					//		}
					//	} else {
					//		selectors[sel.Id] = sel
					//		t.AllMethods = append(t.AllMethods, sel)
					//	}
					//}

					// ToDo: verify the correctness of the following implementation.

					//d.collectSelectorsForInterfaceType(sel.Field.Type, depth+1, currentCounter, smm)
					//for _, sel := range sel.Field.Type.AllMethods {
					ut := sel.Field.Type.Underlying

					// The true is needed.
					//if true || !d.collectSelectorsForInterfaceType(ut, depth+1, currentCounter, smm) {
					d.collectSelectorsForInterfaceType(ut, depth+1, currentCounter, smm)
					if true {
						for _, sel := range ut.AllMethods {
							if old, ok := selectors[sel.Id]; ok {
								if old.Method.Type != sel.Method.Type {
									panic("overlapped interface methods but signatures are different")
								} else {
									// ToDo: The current implementation does not always find true overlappings.
									//log.Println("overlapping interface method:", sel.Id, ". (allowed since Go 1.14)")
								}
							} else {
								selectors[sel.Id] = sel
								t.AllMethods = append(t.AllMethods, sel)
							}
						}
					}
				}

			}

			//log.Println(depth, "===", len(t.DirectSelectors), len(t.AllMethods), t.TT)
		}
	}

	return
}

func (d *CodeAnalyzer) collectSelectorsFroNonInterfaceType(t *TypeInfo, smm *SeleterMapManager, checkedTypes map[uint32]uint16) {

	if (t.attributes & promotedSelectorsCollected) != 0 {
		return
	}

	defer func() {
		t.attributes |= promotedSelectorsCollected
	}()

	var namedType *TypeInfo
	var structType *TypeInfo

	switch t.TT.(type) {
	case *types.Named:
		namedType = t

		switch t.Underlying.TT.(type) {
		case *types.Struct:
			structType = t.Underlying
			break
		case *types.Interface:
			// already done in collectSelectorsForInterfaceType.
			return
		case *types.Pointer:
			// named pointer types have no selectors.
			return
		default:
			t.AllMethods = t.DirectSelectors
			// no promoted selectors to collect.
			return
		}
	case *types.Struct:
		structType = t
		break
	case *types.Interface:
		// already done in collectSelectorsForInterfaceType.
		return
	case *types.Pointer:
		// selectors of *T will be recoreded in T.selectors, except T is an interface or pointer.
		return
	default:
		// Basics and other unnamed types have no selectors.
		return
	}

	if structType == nil {
		panic("should not")
	}

	if namedType == nil {
		//debug := false
		//if len(structType.DirectSelectors) == 1 && structType.DirectSelectors[0].Name() == "C" {
		//	debug = true
		//}
		//if debug {
		//	log.Println("================================== structType=", structType)
		//}

		numEmbeddeds := 0
		for _, sel := range structType.DirectSelectors {
			if sel.Field == nil {
				panic("should not")
			}
			if sel.Field.Mode != EmbedMode_None {
				numEmbeddeds++
			}
		}

		// The simple case.
		if numEmbeddeds == 0 {
			t.AllFields = t.DirectSelectors
			if namedType != nil {
				t.AllMethods = t.DirectSelectors
			}

			// no promoted selectors, so return.
			return
		}

		// ...
		defer func() {
			for k := range checkedTypes {
				delete(checkedTypes, k)
			}
		}()

		// map[string]*Selector
		selectorMap := smm.apply()
		defer smm.release(selectorMap)

		selectorList := list.New()
		defer func() {
			numFields, numMethods := 0, 0
			for e := selectorList.Front(); e != nil; e = e.Next() {
				sel := e.Value.(*Selector)
				if sel.cond != SelectorCond_Hidden {
					//t.AllFields = append(t.AllFields, sel)
					if sel.Field != nil {
						numFields++
					} else {
						numMethods++
					}
				}
			}

			t.AllFields = make([]*Selector, 0, numFields)
			t.AllMethods = make([]*Selector, 0, numMethods)

			for e := selectorList.Front(); e != nil; e = e.Next() {
				sel := e.Value.(*Selector)
				if sel.cond != SelectorCond_Hidden {
					if sel.Field != nil {
						t.AllFields = append(t.AllFields, sel)
					} else { // if sel.Method != nil
						t.AllMethods = append(t.AllMethods, sel)
					}
				}
			}
		}()

		// Collect direct fields
		//structType.counter = currentCounter
		checkedTypes[structType.index] = 0
		for _, sel := range structType.DirectSelectors {
			if _, exist := selectorMap[sel.Id]; exist {
				panic("should not")
			} else {
				selectorMap[sel.Id] = sel
				selectorList.PushBack(sel)
			}
		}

		//log.Println("number of direct selectors:", selectorList.Len())

		// Returns how many new promoted embedded fields are inserted. (Not quite useful acctually.)
		var collectSelectorsFromEmbeddedField = func(embeddedField *Selector, insertAfter *list.Element) (numNewPromotedEmbeddedFields int) {

			depth := embeddedField.Depth + 1

			////if embeddedField.Field.Type.counter == currentCounter {
			////	return
			////}
			////embeddedField.Field.Type.counter = currentCounter
			//if d, checked := checkedTypes[embeddedField.Field.Type.index]; checked && d < depth {
			//	return
			//}
			//checkedTypes[embeddedField.Field.Type.index] = depth
			// Will do it below.

			embeddingChain := &EmbeddedField{
				Field: embeddedField.Field,
				Prev:  embeddedField.EmbeddingChain,
			}

			collect := func(t *TypeInfo, selectors []*Selector, indrect bool) {
				mustConflict := false
				if d, checked := checkedTypes[t.index]; checked {
					if d > depth {
						panic("impossible")
					}
					if d < depth {
						// no needs to continue
						return
					}
					mustConflict = true
					//log.Println("         old >>>", depth, d, t)
				} else {
					checkedTypes[t.index] = depth
					//log.Println("         new >>>", depth, t)
				}

				//log.Println("             >>>", len(selectors))

				for _, sel := range selectors {
					//log.Println("         ???", sel.Id)
					newCond := SelectorCond_Normal
					if old, exist := selectorMap[sel.Id]; exist {
						if old.Depth == depth {
							//log.Println("         !!! collide", sel.Id)
							old.cond = SelectorCond_Hidden
						} else if old.cond == SelectorCond_Normal { // old.Depth < depth
							//log.Println("         !!! shadow", sel.Id)
							//old.cond = SelectorCond_Shadowing
						}
						newCond = SelectorCond_Hidden
					} else {
						if mustConflict {
							panic("not conflict?! " + sel.Id)
						}
					}

					new := &Selector{
						Id:             sel.Id,
						Field:          sel.Field,
						Method:         sel.Method,
						EmbeddingChain: embeddingChain,
						Depth:          depth,
						Indirect:       embeddedField.Indirect || indrect,
						cond:           newCond,
					}
					selectorMap[sel.Id] = new
					insertAfter = selectorList.InsertAfter(new, insertAfter)
					//log.Println("         !!! add", new.Id)

					if new.Field != nil && new.Field.Mode != EmbedMode_None {
						numNewPromotedEmbeddedFields++
					}
				}
			}

			//log.Println("       000")
			switch t := embeddedField.Field.Type; tt := t.TT.(type) {
			case *types.Named:
				switch t.Underlying.TT.(type) {
				case *types.Struct:
					//log.Println("       111 aaa")
					// Collect direct methods
					collect(t, t.DirectSelectors, false)
					// Collect direct fields
					collect(t.Underlying, t.Underlying.DirectSelectors, false)
				case *types.Interface:
					//log.Println("       111 bbb")
					// Collect all methods
					collect(t, t.AllMethods, false) // <=> t.Underlying.AllMethods
				case *types.Pointer:
					//log.Println("       111 ccc")
					// named pointer types have no selectors.
				default:
					//log.Println("       111 ddd")
					// Collect direct methods
					collect(t, t.DirectSelectors, false)
				}
			case *types.Struct:
				//log.Println("       222")
				// Collect direct fields
				collect(t, t.DirectSelectors, false)
			case *types.Interface:
				//log.Println("       333")
				// Collect all methods
				collect(t, t.AllMethods, false)
			case *types.Pointer:
				//log.Println("       444")
				baseType := d.RegisterType(tt.Elem())
				switch baseTT := baseType.TT.(type) {
				case *types.Struct:
					//log.Println("       444 aaa")
					// Collect direct fields
					collect(baseType, baseType.DirectSelectors, true)
				case *types.Named:
					switch baseType.Underlying.TT.(type) {
					case *types.Struct:
						//log.Println("       444 bbb 111")
						// Collect direct methods
						collect(baseType, baseType.DirectSelectors, true)
						// Collect direct fields
						collect(baseType.Underlying, baseType.Underlying.DirectSelectors, true)
					case *types.Interface, *types.Pointer:
						//log.Println("       444 bbb 222")
						// None to collect. Not embeddable actually.
					default:
						//log.Println("       444 bbb 333")
						// Collect direct methods
						collect(baseType, baseType.DirectSelectors, true)
					}
				default:
					_ = baseTT
					//log.Println("       444 bbb 444", baseTT)
				}
			default:
				//log.Println("      555", tt)
			}

			return
		}

		for depth := uint16(0); ; depth++ {
			//if debug {
			//	log.Println("   ~~~ depth=", depth)
			//}
			needToCheckDeepers := false
			for e := selectorList.Front(); e != nil; e = e.Next() {
				sel := e.Value.(*Selector)
				//if debug {
				//	log.Println("     - sel=", sel.Id)
				//}
				if sel.Depth != depth || sel.Field == nil || sel.Field.Mode == EmbedMode_None {
					continue
				}

				collectSelectorsFromEmbeddedField(sel, e)
				needToCheckDeepers = true
			}
			if !needToCheckDeepers {
				break
			}
		}

		return
	}

	d.collectSelectorsFroNonInterfaceType(structType, smm, checkedTypes)

	// This line is nonsense.
	//namedType.counter = currentCounter // <=> t.counter = currentCounter
	//checkedTypes[namedType.index] = 0

	// map[string]*Selector
	selectorMap := smm.apply()
	defer smm.release(selectorMap)

	// ...
	t.AllMethods = make([]*Selector, 0, len(namedType.DirectSelectors)+len(structType.AllMethods))
	t.AllFields = make([]*Selector, 0, len(structType.AllFields))

	// Direct declared methods.
	for _, sel := range namedType.DirectSelectors {
		if _, exist := selectorMap[sel.Id]; exist {
			panic("should not")
		} else {
			selectorMap[sel.Id] = sel
			t.AllMethods = append(t.AllMethods, sel)
		}
	}

	// Promoted methods.
	for _, sel := range structType.AllMethods {
		if _, exist := selectorMap[sel.Id]; exist {
			// log.Println(sel.Id, "is shadowed")
		} else {
			selectorMap[sel.Id] = sel
			t.AllMethods = append(t.AllMethods, sel)
		}
	}

	// Fields, including promoteds.
	for _, sel := range structType.AllFields {
		if _, exist := selectorMap[sel.Id]; exist {
			// log.Println(sel.Id, "is shadowed")
		} else {
			selectorMap[sel.Id] = sel
			t.AllFields = append(t.AllFields, sel)
		}
	}
}

/*

Sell points:
* show implementions
* show promoted selectors, even on unexported embedded fields.
* show exported selectors on unexported elements
* show unpexorted resource
* better custom builtin package page
* rich code view functionalities
* pure css interactive, no javascript involoved.

// Front page:
// * N typed recorded, in which M are nameds, P are exporteds.
// * Z aliases. K exported.
// * X interfaces (Y nameds),
// Main packages: ...

// Do not ue the offiical builtin package page? Too many quirks. Use a simple custom page instead?
// * make(Type ChannelKind|MapKind|SliceKind) Type
     Type must denote a channel, map, or slice type.
//   make(Type ChannelKind|MapKind|SliceKind, size integer) Type
     Type must denote a channel, map, or slice type.
     size must be a non-negative integer value (of any integer type) or a literal denoting a non-negative integer value.
//   make(Type SliceKind, length integer, capacity interger) Type
     Type must denote a slice type.
     length and capacity must be both non-negative integer values (of any integer type) or literals denoting a non-negative integer values.
     The types of length and capacity may be different.
// * new(Type AnyKind) *Type
// * each with simple examples

// An interesting idea: service docs from GOPROXY server.
// Still not perfect as p2p solutions.

// Code view:
// 1. when click param/result names, hightlight all their respective uses.
// 2. when click function call, jump to definition.
// 3. when click function/variable/constant declaration, show reference list.
// 4. CTRL+click go to doc page.
func (type2 *UnsafeArrayType) SetIndex(obj interface{}, index int, elem interface{}) {
	objEFace := unpackEFace(obj)
	assertType("ArrayType.SetIndex argument 1", type2.ptrRType, objEFace.rtype)
	elemEFace := unpackEFace(elem)
	assertType("ArrayType.SetIndex argument 3", type2.pElemRType, elemEFace.rtype)
	type2.UnsafeSetIndex(objEFace.data, index, elemEFace.data)
}

filter: same signatures | common returns | common parameters
func (...) (...)
(still not perfect)
In any place, when click a whole unname type literal,
show a all decomposed types in the a below div.
Click each decoposed type, show actions: as parameters of, as results of, as fields of.

chekboxes: promoteds (default on) | unexporteds (default off)
type S struct {
	y int
	*T
	  .x int
	X
	  .z int
}

chekboxes: expand embeddeds (default off) | unexporteds (default off)
type I interface {
	M1()
	m2()
	M3()

	// Click an embedded type name, make the above involoved methods highlighted.
	// (And might show methods with different docs and comments.)
	Ia; Ib; Ic
}

Unnamed types: show up at 1/2/3/..., and in other packages 7/8/9/...
      filters: as inputs | as outputs | as field types
struct {
	a int
	b string
}

// an expanded method might comes from two embedded interfaces,
// their docs might be different.

tabs (for interface): returned by | as inputs of | implemented by | subset of

tabs (for non-interface): methods | embedded by | returned by | as inputs of | implements

Methods:
	(pointer)           func (s S) M1()
	(promoted, pointer) func (s S) M2()
		             (a shorthand of T.M2)
		             (... show docs of func (t T) M1())

Embedded by N types.

Returned by:
	func F1() S
	func (x X) M() *S
*/

/*
// Depth-first search
func collectSelectors(t *TypeInfo, checkedselectors map, depth int) {
	t.selectors = make(map[string]*Selector)

	embeddedFields := make([]*TypeName, 0, 100)

	for field : fields {
		if sel, ok := t.selectors[field.Name]; !ok {
			selectors[filed.Name] = NewSelector(...)
			if field is Embeded {
				embeddedFields = append(embeddedFields, field.Type)
			}
		} else if sel != nil && sel.depth == depth {
			t.selectors[field.Name] = nil
		}
	}
	for method : methods {
		...
	}
	for et : embeddedFields {
		collectSelectors(et, t.selectors, depth+1)
	}

	// Remove collided selectors.
	for k, s : t.selectors {
		if s == nil {
			delete(t.selectors, k)
		}
	}
}

// Breadth-first search (better, but need sorting in the end).
// Use a tree to replace list?
func collectSelectors(t *TypeInfo, checkedselectors map, depth int) {
	t.selectors = make(map[string]*Selector)

	embeddedFields := make([]*TypeName, 0, 100)

	for field : fields {
		if sel, ok := t.selectors[field.Name]; !ok {
			selectors[filed.Name] = NewSelector(...)
			if field is Embeded {
				collectSelectors(et, t.selectors, depth+1)
			}
		} else if sel != nil && sel.depth == depth {
			t.selectors[field.Name] = nil
		}
	}
	for method : methods {
		...
	}

	// Remove collided selectors.
	for k, s : t.selectors {
		if s == nil {
			delete(t.selectors, k)
		}
	}
}
*/

func (d *CodeAnalyzer) confirmPackageModules() {
	// Two cases:
	// 1. check the .../vendor/modules.txt files
	// 2. check GOPATH/pkg/mod/...

	// # list all module dependency relations
	// go mod graph
	//	k8s.io/kubernetes sigs.k8s.io/yaml@v1.1.0
	//	...
	//	k8s.io/apiserver@v0.0.0 go.uber.org/zap@v1.10.0
	//	...

	// # from Michael Matloob
	// go list -f '{{.Module.Path}} {{.Module.Dir}}' pkg-import-path

	// # list all involved modules
	// go list -m all
	//	volcano.sh/volcano
	//	modernc.org/xc v1.0.0 => modernc.org/xc v1.0.0
	//	...
	// go list -f '{{.ImportPath}} {{.Module}}' all
	//	go101.org/gold/tests/n go101.org/gold
	//	golang.org/x/mod/internal/lazyregexp golang.org/x/mod v0.1.1-0.20191105210325-c90efee705ee
	//	unsafe <nil>
	//	vendor/golang.org/x/crypto/chacha20 <nil>
	//	...
	// go list -json all
	//	.Module
	//
	// Maybe, it is still better to analyze it manauuly.
	// Temp not to show module pages, module info is only used to find asParamsOf/asResultsOf

	// # get the module at CWD
	// go list -m
	//	volcano.sh/volcano
	//   or
	//	go list -m: not using modules

	// # find GOROOT to find std module info
	// go env

	//findPkgModule := func(pkg *Package) {
	//	// d.stdPackages
	//}
	//_ = findPkgModule

	//for _, pkg := range d.packageList {
	//	if len(pkg.PPkg.GoFiles) == 0 {
	//		continue
	//	}
	//	dir := filepath.Dir(pkg.PPkg.GoFiles[0])
	//	filename := filepath.Join(dir, "go.mod")
	//	filedata, err := ioutil.ReadFile(filename)
	//	if err != nil {
	//		if !errors.Is(err, os.ErrNotExist) {
	//			log.Printf("ioutil.ReadFile %s error: %s", filename, err)
	//		}
	//		continue
	//	}
	//
	//	modFile, err := modfile.ParseLax(filename, filedata, nil)
	//	if err != nil {
	//	}
	//
	//	mod := Module{
	//		Dir:     dir,
	//		Root:    modFile.Module.Mod.Path,
	//		Version: modFile.Module.Mod.Version,
	//	}
	//
	//	_ = mod
	//}

	// I decided to delay the impplementation of this funciton now.
	// One intention to confirm module information is
	// to support module pages, but this is not very essential.
	// Another intention to confirm module information
	// is to calculate the distances of pacakges.
	// However, it might be not perfect to determine
	// the distance of two packages by checking if
	// they are in the same module.
	//
	// The module info confirmed in this funciton will be only for showing.
}

func (d *CodeAnalyzer) sortPackagesByDependencies() {
	var seen = make(map[string]struct{}, len(d.packageList))
	var calculatePackageDepLevel func(pkg *Package)
	calculatePackageDepLevel = func(pkg *Package) {
		if _, ok := seen[pkg.Path()]; ok {
			return
		}
		seen[pkg.Path()] = struct{}{}

		var max = 0
		for _, dep := range pkg.Deps {
			calculatePackageDepLevel(dep)
			if dep.DepLevel > max {
				max = dep.DepLevel
			} else if dep.DepLevel == 0 {
				log.Println("sortPackagesByDependencies: the dep.DepLevel is not calculated yet!")
			}
		}
		pkg.DepLevel = max + 1
	}

	for _, pkg := range d.packageList {
		calculatePackageDepLevel(pkg)
	}

	sort.Slice(d.packageList, func(i, j int) bool {
		return d.packageList[i].DepLevel < d.packageList[j].DepLevel
	})

	for i, pkg := range d.packageList {
		pkg.Index = i
	}
}

func (d *CodeAnalyzer) analyzePackage_CollectDeclarations(pkg *Package) {
	if pkg.PackageAnalyzeResult != nil {
		panic(pkg.Path() + " already analyzed")
	}

	//log.Println("[analyzing]", pkg.Path(), pkg.PPkg.Name)

	pkg.PackageAnalyzeResult = NewPackageAnalyzeResult()

	registerTypeName := func(tn *TypeName) {
		pkg.PackageAnalyzeResult.AllTypeNames = append(pkg.PackageAnalyzeResult.AllTypeNames, tn)
		d.RegisterTypeName(tn)
	}

	registerVariable := func(v *Variable) {
		pkg.PackageAnalyzeResult.AllVariables = append(pkg.PackageAnalyzeResult.AllVariables, v)
	}

	registerConstant := func(c *Constant) {
		pkg.PackageAnalyzeResult.AllConstants = append(pkg.PackageAnalyzeResult.AllConstants, c)
	}

	registerImport := func(imp *Import) {
		pkg.PackageAnalyzeResult.AllImports = append(pkg.PackageAnalyzeResult.AllImports, imp)
	}

	registerFunction := func(f *Function) {
		pkg.PackageAnalyzeResult.AllFunctions = append(pkg.PackageAnalyzeResult.AllFunctions, f)
		d.RegisterFunction(f)
	}

	_ = registerFunction
	_ = registerVariable
	_ = registerConstant
	_ = registerImport

	var isBuiltinPkg = pkg.Path() == "builtin"
	var isUnsafePkg = pkg.Path() == "unsafe"

	// ToDo: use info.TypeOf, info.ObjectOf

	for _, file := range pkg.PPkg.Syntax {

		//ast.Inspect(file, func(n ast.Node) bool {
		//	log.Printf("%T\n", n)
		//	return true
		//})

		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				if fd.Name.Name == "_" {
					continue
				}

				//log.Printf("func %s", fd.Name.Name)
				//log.Printf("(%s) %s (%s) (%s)", fd.Recv, fd.Name.Name, fd.Type.Params, fd.Type.Results)

				// It looks the funciton delcared in "builtin" are types.Func, instead of types.Builtin.
				// But the funcitons declared in "unsafe" are types.Builtin.

				var f *Function

				obj := pkg.PPkg.TypesInfo.Defs[fd.Name]
				switch funcObj := obj.(type) {
				default:
					panic("not a types.Func")
				case *types.Func:
					f = &Function{
						Func: funcObj,

						Pkg:     pkg,
						AstDecl: fd,
					}
					//log.Println("    ", funcObj.Type())
				case *types.Builtin:
					// unsafe ones.
					// ToDo: maybe it is good to manually create a types.Func for each the builtin object.

					f = &Function{
						Builtin: funcObj,

						Pkg:     pkg,
						AstDecl: fd,
					}
					//log.Println("    ", funcObj.Type())
				}
				registerFunction(f)
			} else if gd, ok := decl.(*ast.GenDecl); ok {
				switch gd.Tok {
				case token.TYPE:
					for _, spec := range gd.Specs {
						typeSpec := spec.(*ast.TypeSpec)
						if typeSpec.Name.Name == "_" {
							continue
						}

						obj := pkg.PPkg.TypesInfo.Defs[typeSpec.Name]
						typeObj, ok := obj.(*types.TypeName)
						if !ok {
							//log.Println(pkg.PPkg.Fset.PositionFor(typeSpec.Pos(), false))
							//log.Println(pkg.PPkg.TypesInfo.Defs)
							panic(fmt.Sprintf("not a types.TypeName: %[1]v, %[1]T. Spec: %v", obj, typeSpec.Name.Name))
						}

						tv := pkg.PPkg.TypesInfo.Types[typeSpec.Type]
						if !tv.IsType() {
							if pkg.Path() != "unsafe" {
								panic(typeSpec.Name.Name + ": not type")
							}

							// Now, unsafe AST expressions are the only ast.Expr(s)
							// which are allowed to not associate with a TypeAndValue.
							// For unsafe, although tv.IsType() == false, tv.Type is valid.
							// See fillUnsafePackage for details.
							if tv.Type == nil {
								panic(typeSpec.Name.Name + ": tv.Type is nil")
							}
						}

						var srcType = tv.Type
						var objName = typeObj.Name()
						// Exported names, such as Type and Type1 are fake types.
						if isBuiltinPkg && !token.IsExported(objName) {
							var ok bool
							// It looks the parsed one are not the internal one.
							//fmt.Println(typeObj == types.Universe.Lookup(objName)) // false
							// Replace it with the internal one.
							typeObj, ok = types.Universe.Lookup(objName).(*types.TypeName)
							if !ok {
								panic("builtin " + objName + " not found")
							}
							//log.Println(srcType, srcType.Underlying(), srcType == srcType.Underlying()) // true

							//srcType = typeObj.Type().Underlying() // why underlying here? error and its underlying is different.
							//log.Println(typeObj.Type(), srcType, typeObj.Type() == srcType) // true
							srcType = typeObj.Type()

							// It looks the below twos are not equal, though
							// types.Idenfical(them) returns true. So, typeObj.Type()
							// and srcType are both internal ByteType, but not Uint8Type.
							// Sometimes, this might matter.
							//
							// ByteType:  types.Universe.Lookup("byte").(*types.TypeName).Type()
							// Uint8Type: types.Universe.Lookup("uint8").(*types.TypeName).Type()
							//
							// The type of a custom aliase is the type it denotes.
							//
							// // true true
							//log.Println("==================",
							//	d.RegisterType(types.Universe.Lookup("byte").(*types.TypeName).Type()) ==
							//		d.RegisterType(types.Universe.Lookup("uint8").(*types.TypeName).Type()),
							//	types.Identical(types.Universe.Lookup("byte").(*types.TypeName).Type(),
							//		types.Universe.Lookup("uint8").(*types.TypeName).Type(),
							//	),
							//)
						}

						srcTypeInfo := d.RegisterType(srcType)
						newTypeInfo := d.RegisterType(typeObj.Type())

						//if isBuiltinPkg && !token.IsExported(objName) {
						//log.Println(typeSpec.Name.Name, srcTypeInfo == newTypeInfo)
						//}

						tn := &TypeName{
							TypeName: typeObj,

							Pkg:     pkg,
							AstDecl: gd,
							AstSpec: typeSpec,
						}
						if typeObj.IsAlias() {
							if srcTypeInfo != newTypeInfo {
								panic(fmt.Sprintf("srcTypeInfo != newTypeInfo, %v, %v", srcTypeInfo, newTypeInfo))
							}

							tn.Alias = &TypeAlias{
								Denoting: srcTypeInfo,
								TypeName: tn,
							}
							srcTypeInfo.Aliases = append(srcTypeInfo.Aliases, tn.Alias)

							if isBuiltinPkg || isUnsafePkg {
								tn.Alias.attributes |= Builtin
							}

							// ToDo: check embeddable

						} else {
							tn.Named = newTypeInfo
							newTypeInfo.TypeName = tn
							if isBuiltinPkg || isUnsafePkg {
								tn.Named.attributes |= Builtin
							}
						}

						registerTypeName(tn)
					}
				case token.VAR:
					for _, spec := range gd.Specs {
						valueSpec := spec.(*ast.ValueSpec)
						//log.Println("var", valueSpec.Names, valueSpec.Type, valueSpec.Values)

						for _, name := range valueSpec.Names {
							if name.Name == "_" {
								continue
							}

							obj := pkg.PPkg.TypesInfo.Defs[name]
							varObj, ok := obj.(*types.Var)
							if !ok {
								panic("not a types.Var")
							}

							v := &Variable{
								Var: varObj,

								Pkg:     pkg,
								AstDecl: gd,
								AstSpec: valueSpec,
							}

							registerVariable(v)
						}
					}
				case token.CONST:
					for _, spec := range gd.Specs {
						valueSpec := spec.(*ast.ValueSpec)
						//log.Println("const", valueSpec.Names, valueSpec.Type, valueSpec.Values)

						for _, name := range valueSpec.Names {
							if name.Name == "_" {
								continue
							}

							obj := pkg.PPkg.TypesInfo.Defs[name]
							constObj, ok := obj.(*types.Const)
							if !ok {
								panic("not a types.Const")
							}

							c := &Constant{
								Const: constObj,

								Pkg:     pkg,
								AstDecl: gd,
								AstSpec: valueSpec,
							}

							registerConstant(c)
						}
					}
				case token.IMPORT:
					// ToDo: importSpec.Name might be nil
					for _, spec := range gd.Specs {
						var obj types.Object
						importSpec := spec.(*ast.ImportSpec)
						if importSpec.Name != nil {
							//log.Println("import 1", importSpec.Name.Name, importSpec.Path.Value)
							obj = pkg.PPkg.TypesInfo.Defs[importSpec.Name]
						} else {
							//log.Println("import 2", importSpec.Path.Value)
							obj = pkg.PPkg.TypesInfo.Implicits[importSpec]
						}
						//log.Println(obj)

						pkgObj, ok := obj.(*types.PkgName)
						if !ok {
							//log.Println(pkg.PPkg.Fset.PositionFor(importSpec.Pos(), false))
							//log.Println(pkg.PPkg.TypesInfo.Implicits)
							panic(fmt.Sprintf("not a types.PkgName: %[1]v, %[1]T. Spec: %v, %v", obj, importSpec.Name, importSpec.Path.Value))
						}

						imp := &Import{
							PkgName: pkgObj,

							Pkg:     pkg,
							AstDecl: gd,
							AstSpec: importSpec,
						}
						registerImport(imp)
					}
				}
			}
		}
	}

	//  We must do the collection work after all types are collected.
	for _, f := range pkg.PackageAnalyzeResult.AllFunctions {
		if f.Func != nil {
			d.registerExplicitlyDeclaredMethod(f)
		}

		if f.IsMethod() && f.AstDecl.Recv != nil {
			if len(f.AstDecl.Recv.List) != 1 {
				panic("should not")
			}
			field := f.AstDecl.Recv.List[0]
			var id *ast.Ident
			switch expr := field.Type.(type) {
			default:
				panic("should not")
			case *ast.Ident:
				id = expr
			case *ast.StarExpr:
				tid, ok := expr.X.(*ast.Ident)
				if !ok {
					panic("should not")
				}
				id = tid
			}
			if !token.IsExported(id.Name) {
				// ToDo: If it is proved that some values of this type are
				//       exposed to other packages, then should not continue here.
				continue
			}
		}
		if f.Exported() {
			d.registerFunctionForInvolvedTypeNames(f)
		}
	}
	for _, v := range pkg.PackageAnalyzeResult.AllVariables {
		if v.Exported() {
			d.registerValueForItsTypeName(v)
		}
	}
	for _, c := range pkg.PackageAnalyzeResult.AllConstants {
		if c.Exported() {
			d.registerValueForItsTypeName(c)
		}
	}
}

func (d *CodeAnalyzer) analyzePackage_CollectSomeRuntimeFunctionPositions() {
	// ...
	if runtimePkg := d.packageTable["runtime"]; runtimePkg != nil {
		fnames := []string{
			"selectgo",      // for select blocks (except one-case-plus-default ones)
			"selectnbsend",  // one-case-plus-default select blocks
			"selectnbrecv",  // select {case v = <-c:; default:}
			"selectnbrecv2", // select {case v, ok = <-c:; default:}
			"chansend",      // c <- v
			"chanrecv1",     // v = <- c
			"chanrecv2",     // v, ok = <-c
			"gopanic",
			"gorecover",
			// gave up other built-in functions
		}
		d.runtimeFuncPositions = make(map[string]token.Position, 32)

		for _, f := range fnames {
			obj := runtimePkg.PPkg.Types.Scope().Lookup(f)
			if obj == nil {
				log.Printf("!!! runtime.%s is not found", f)
			}
			d.runtimeFuncPositions[f] = runtimePkg.PPkg.Fset.PositionFor(obj.Pos(), false)
		}
	}
}

func (d *CodeAnalyzer) analyzePackage_FindTypeSources(pkg *Package) {
	var isBuiltin = pkg.Path() == "builtin"

	//log.Println("[analyzing]", pkg.Path(), pkg.PPkg.Name)
	for _, file := range pkg.PPkg.Syntax {

		//ast.Inspect(file, func(n ast.Node) bool {
		//	log.Printf("%T\n", n)
		//	return true
		//})

		for _, decl := range file.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
				for _, spec := range gd.Specs {
					typeSpec := spec.(*ast.TypeSpec)

					obj := pkg.PPkg.TypesInfo.Defs[typeSpec.Name]
					typeObj := obj.(*types.TypeName)
					if typeObj.Name() == "_" {
						continue
					}

					newTypeName := d.allTypeNameTable[d.Id2(typeObj.Pkg(), typeObj.Name())]
					if newTypeName == nil {
						panic("type name " + typeSpec.Name.Name + " not found: " + d.Id1(typeObj.Pkg(), typeObj.Name()))
					}

					var findSource func(ast.Expr, bool)
					findSource = func(srcNode ast.Expr, startSource bool) {
						var source *TypeSource
						if startSource {
							if newTypeName.StarSource == nil {
								newTypeName.StarSource = &TypeSource{}
							}
							source = newTypeName.StarSource
						} else {
							source = &newTypeName.Source
						}

						var sttNode *ast.StructType
						var ittNode *ast.InterfaceType

						switch expr := srcNode.(type) {
						case *ast.Ident:

							//log.Println("???", d.Id(pkg.PPkg.Types, expr.Name))

							//log.Println("   ", pkg.PPkg.TypesInfo.ObjectOf(expr))

							srcObj := pkg.PPkg.TypesInfo.ObjectOf(expr)
							if srcObj == nil {
								if pkg.Path() != "unsafe" {
									panic("srcObj is nil but package is not unsafe")
								}
								return
							}
							srcTypeObj := srcObj.(*types.TypeName)

							//log.Println("   srcTypeObj.Pkg() =", srcTypeObj.Pkg())
							// if srcType is a built type, srcTypeObj.Pkg() == nil

							tn := d.allTypeNameTable[d.Id2(srcTypeObj.Pkg(), expr.Name)]
							if tn == nil {
								panic("type name " + expr.Name + " not found")
							}
							source.TypeName = tn

							//log.Println(startSource, "ident,", pkg.Path()+"."+typeSpec.Name.Name, "source is:", tn.Pkg.Path()+"."+expr.Name)

							return
						case *ast.SelectorExpr:
							//log.Println("selector,", pkg.Path()+"."+typeSpec.Name.Name, "source is:")
							srcObj := pkg.PPkg.TypesInfo.ObjectOf(expr.X.(*ast.Ident))
							srcPkg := srcObj.(*types.PkgName)

							tn := d.allTypeNameTable[d.Id2(srcPkg.Imported(), expr.Sel.Name)]
							if tn == nil {
								panic("type name " + expr.Sel.Name + " not found")
							}
							source.TypeName = tn

							//log.Println(startSource, "selector,", pkg.Path()+"."+typeSpec.Name.Name, "source is:", tn.Pkg.Path()+"."+expr.Sel.Name)
							return
						case *ast.ParenExpr:
							//log.Println("paren,", pkg.Path()+"."+typeSpec.Name.Name, "source is:")
							findSource(expr.X, false)
							return
						case *ast.StarExpr:
							if !startSource {
								//log.Println("star,", pkg.Path()+"."+typeSpec.Name.Name, "source is:")
								findSource(expr.X, true)
								return
							}
						case *ast.StructType:
							sttNode = expr
						case *ast.InterfaceType:
							ittNode = expr
						}

						// ToDo: don't use the std go/types and go/pacakges packages.
						//       Now, uint8 and byte are treat as two types by go/types.
						//       Write a custom one tailored for docs and code analyzing.
						//       Run "go mod tidy" before running gold using the custom packages
						//       to ensure all modules are cached locally.

						tv := pkg.PPkg.TypesInfo.Types[srcNode]
						srcTypeInfo := d.RegisterType(tv.Type)
						source.UnnamedType = srcTypeInfo
						//log.Println(startSource, "default,", pkg.Path()+"."+typeSpec.Name.Name, "source is:", tv.Type)

						if sttNode != nil {
							d.registerDirectFields(srcTypeInfo, sttNode, pkg)
						} else if ittNode != nil {
							if isBuiltin && typeSpec.Name.Name == "error" {
								/*
									//errorTN, _ := types.Universe.Lookup("error").(*types.TypeName)
									//errotUT := d.RegisterType(errorTN.Type().Underlying())
									//d.registerExplicitlySpecifiedMethods(errotUT, ittNode, pkg)

									//log.Println("=============== old:", srcTypeInfo.index)
									// This one is the type shown in the builtin.go source code,
									// not the one in type.Universal package. This one is only for docs purpose.
									// ToDo: use custom builtin page, remove the special handling.
									d.registerExplicitlySpecifiedMethods(srcTypeInfo, ittNode, pkg)

									// ToDo: load builtin.error != universal.error
									srcTypeInfo = d.RegisterType(newTypeName.Named.TT.Underlying())

									//log.Println("===============", errotUT.index, srcTypeInfo.index)
								*/

								d.registerExplicitlySpecifiedMethods(srcTypeInfo, ittNode, pkg)
								srcTypeInfo = d.RegisterType(types.Universe.Lookup("error").(*types.TypeName).Type().Underlying())
							}
							d.registerExplicitlySpecifiedMethods(srcTypeInfo, ittNode, pkg)
						}
					}
					findSource(typeSpec.Type, false)
				}
			}
		}
	}

	return
}

type Module struct {
	Dir     string
	Root    string // root import path
	Version string
}

type Package struct {
	Index int
	PPkg  *packages.Package

	Mod      *Module
	Deps     []*Package
	DepLevel int // 0 means the level is not determined yet
	DepedBys []*Package

	// This field might be shared with PackageForDisplay
	// for concurrenct reads.
	*PackageAnalyzeResult
}

func (p *Package) Path() string {
	return p.PPkg.PkgPath // might be prefixed with "vendor/", which is different from import path.
}

type PackageAnalyzeResult struct {
	AllTypeNames []*TypeName
	AllFunctions []*Function
	AllVariables []*Variable
	AllConstants []*Constant
	AllImports   []*Import
	SourceFiles  []SourceFileInfo
}

func NewPackageAnalyzeResult() *PackageAnalyzeResult {
	// ToDo: maybe it is better to run a statistic phase firstly,
	// so that the length of each slice will get knowledged.
	return &PackageAnalyzeResult{
		AllTypeNames: make([]*TypeName, 0, 64),
		AllFunctions: make([]*Function, 0, 64),
		AllVariables: make([]*Variable, 0, 64),
		AllConstants: make([]*Constant, 0, 64),
		AllImports:   make([]*Import, 0, 64),
	}
}

// ToDo: better to maintain a global sourceFilePath => SourceFileInfo table?
func (r *PackageAnalyzeResult) SourceFileInfo(srcPath string) *SourceFileInfo {
	for _, info := range r.SourceFiles {
		if info.OriginalGoFile == srcPath {
			return &info
		}
		if info.GeneratedFile == srcPath {
			return &info
		}
	}
	return nil
}

type RefPos struct {
	Pkg *Package
	Pos token.Pos
}

type AstNode struct {
	Pkg  *Package
	Node ast.Node
}

type Resource interface {
	Name() string
	Exported() bool
	//IndexString() string
	Documentation() string
	Comment() string
	Position() token.Position
	Package() *Package
}

type ValueResource interface {
	Resource
	TType() types.Type // The result should not be used in comparisons.
	TypeInfo(d *CodeAnalyzer) *TypeInfo
}

type Attribute uint32

const (
	// Runtime only flags.
	analyseCompleted Attribute = 1 << (31 - iota)
	directSelectorsCollected
	promotedSelectorsCollected

	// Higher bits are for runtime-only flags.
	AtributesPersistentMask Attribute = (1 << 25) - 1

	// Caching individual packages seperately might be not a good idea.
	// There are many complexities here.
	// * implementation relations become larger along with more packages are involved.
	// Caching by arguments starting packages, as one file, is simpler.

	// For functions, type aliases and named types.
	Builtin Attribute = 1 << 0

	// For type aliases and named types.
	Embeddable    Attribute = 1 << 1
	PtrEmbeddable Attribute = 1 << 2

	// For unnamed struct and interface types.
	HasUnexporteds Attribute = 1 << 3

	// For all types.
	Defined    Attribute = 1 << 4
	Comparable Attribute = 1 << 5

	// For channel types.
	Sendable   Attribute = 1 << 6
	Receivable Attribute = 1 << 7

	// For funcitons.
	Variadic Attribute = 1 << 8
)

type TypeSource struct {
	TypeName    *TypeName
	UnnamedType *TypeInfo
}

//func (ts *TypeSource) Denoting(d *CodeAnalyzer) *TypeInfo {
//	if ts.UnnamedType != nil {
//		return ts.UnnamedType
//	}
//	return ts.TypeName.Denoting(d)
//}

type TypeName struct {
	// One and only one of the two is nil.
	Alias *TypeAlias
	Named *TypeInfo

	//index uint32 // the global index

	// ToDo: simplify the source defintion.
	// Four kinds of sources to affect promoted selectors:
	// 1. typename
	// 2. *typename
	// 3. unnamed type
	// 4. *unname type
	Source     TypeSource
	StarSource *TypeSource

	UsePositions []token.Position

	*types.TypeName

	index uint32 // ToDo: any useful?

	Pkg     *Package // some duplicated with types.TypeName.Pkg(), except builtin types
	AstDecl *ast.GenDecl
	AstSpec *ast.TypeSpec
}

//func (tn *TypeName) IndexString() string {
//	var b strings.Builder
//
//	b.WriteString(tn.Name())
//	if tn.Alias != nil {
//		b.WriteString(" = ")
//	} else {
//		b.WriteString(" ")
//	}
//	WriteType(&b, tn.AstSpec.Type, tn.Pkg.PPkg.TypesInfo, true)
//
//	return b.String()
//}

//func (tn *TypeName) Id() string {
//	return tn.obj.Id()
//}

//func (tn *TypeName) Name() string {
//	return tn.obj.Name()
//}

func (tn *TypeName) Exported() bool {
	if tn.Pkg.Path() == "builtin" {
		return !token.IsExported(tn.Name())
	}
	return tn.TypeName.Exported()
}

func (tn *TypeName) Position() token.Position {
	return tn.Pkg.PPkg.Fset.PositionFor(tn.AstSpec.Name.Pos(), false)
}

func (tn *TypeName) Documentation() string {
	//doc := tn.AstDecl.Doc.Text()
	//if t := tn.AstSpec.Doc.Text(); t != "" {
	//	doc = doc + "\n\n" + t
	//}
	//return doc
	doc := tn.AstSpec.Doc.Text()
	if doc == "" {
		doc = tn.AstDecl.Doc.Text()
	}
	return doc
}

func (tn *TypeName) Comment() string {
	return tn.AstSpec.Comment.Text()
}

func (tn *TypeName) Package() *Package {
	return tn.Pkg
}

//func (tn *TypeName) Comment() string {
//	return tn.AstSpec.Comment.Text()
//}

//func (tn *TypeName) Denoting(d *CodeAnalyzer) *TypeInfo {
//	if tn.Named != nil {
//		return tn.Named
//	}
//
//	if tn.StarSource != nil {
//		return d.RegisterType(types.NewPointer(tn.StarSource.Denoting(d).TT))
//	}
//
//	return tn.Source.Denoting(d)
//}

func (tn *TypeName) Denoting() *TypeInfo {
	if tn.Named != nil {
		return tn.Named
	}

	return tn.Alias.Denoting
}

//func (tn *TypeName) Underlying(d *CodeAnalyzer) *TypeInfo {
//	if tn.StarSource != nil || tn.Source.UnnamedType != nil {
//		return tn.Denoting(d)
//	}
//	return tn.Source.TypeName.Underlying(d)
//}

type TypeAlias struct {
	Denoting *TypeInfo

	// For named and basic types.
	TypeName *TypeName

	// Builtin, Embeddable.
	attributes Attribute
}

//func (a *TypeAlias) Embeddable() bool {
//	var tc = a.Denoting.Common()
//	if tc.Attributes&Embeddable != 0 {
//		return true
//	}
//	if tc.Kind != Pointer {
//		return false
//	}
//	if _, ok := a.Denoting.(*Type_Named); !ok {
//		return false
//	}
//	tc = a.Denoting.(*Type_Pointer).Common()
//	return tc.Kind&(Ptr|Interface) == 0
//}

type TypeInfo struct {
	TT types.Type

	Underlying *TypeInfo

	//Implements     []*TypeInfo
	///StarImplements []*TypeInfo // if TT is neither pointer nor interface.
	Implements []Implementation

	// For interface types.
	ImplementedBys []*TypeInfo

	// For builtin and unnamed types only.
	Aliases []*TypeAlias

	// For named and basic types.
	TypeName *TypeName

	// For unnamed types.
	UsePositions []token.Position

	// For unnamed interfaces and structs, this field must be nil.
	//Pkg *Package // Looks this field is never used. (It really should not exist in this type.)

	// Including promoted ones. For struct types only.
	// * For named types, only explicitly declared methods are included.
	//   The field is only built for T. (*T).DirectSelectors is always nil.
	// * For named interface types, all explicitly specified methods and embedded types (as fields).
	// * For unnamed struct types, only direct fields. Only built for strct{...}, not for *struct{...}.
	DirectSelectors []*Selector

	// All methods, including extended/promoted ones.
	AllMethods []*Selector

	// All fields, including promoted ones.
	AllFields []*Selector

	// Including promoted ones. For both T and *T.
	//Methods []*Method

	// For .TypeName != nil
	AsTypesOf   []ValueResource // variables and constants
	AsInputsOf  []ValueResource // variables and functions
	AsOutputsOf []ValueResource // variables and functions
	// ToDo: register variables (of function types) for AsInputsOf and AsOutputsOf

	attributes Attribute // ToDo: fill the bits

	// The global type index. It will be
	// used in calculating method signatures.
	// ToDo: check if it is problematic to allow index == 0.
	index uint32

	// Used in several scenarios.
	counter uint32
	//counter2 int32
}

type Implementation struct {
	Impler    *TypeInfo // a struct or named type (same as the owner), or a pointer to such a type
	Interface *TypeInfo // an interface type
}

type Import struct {
	*types.PkgName

	Pkg     *Package // some duplicated with types.PkgName.Pkg()
	AstDecl *ast.GenDecl
	AstSpec *ast.ImportSpec
}

type Constant struct {
	*types.Const

	Type    *TypeInfo
	Pkg     *Package // some duplicated with types.Const.Pkg()
	AstDecl *ast.GenDecl
	AstSpec *ast.ValueSpec
}

func (c *Constant) Position() token.Position {
	for _, n := range c.AstSpec.Names {
		if n.Name == c.Name() {
			return c.Pkg.PPkg.Fset.PositionFor(n.Pos(), false)
		}
	}
	panic("should not")
}

func (c *Constant) Documentation() string {
	doc := c.AstSpec.Doc.Text()
	if doc == "" {
		doc = c.AstDecl.Doc.Text()
	}
	return doc
}

func (c *Constant) Comment() string {
	return c.AstSpec.Comment.Text()
}

func (c *Constant) Package() *Package {
	return c.Pkg
}

func (c *Constant) Exported() bool {
	if c.Pkg.Path() == "builtin" {
		return !token.IsExported(c.Name())
	}
	return c.Const.Exported()
}

func (c *Constant) TType() types.Type {
	return c.Const.Type()
}

func (c *Constant) TypeInfo(d *CodeAnalyzer) *TypeInfo {
	if c.Type == nil {
		c.Type = d.RegisterType(c.TType())
	}
	return c.Type
}

//func (c *Constant) IndexString() string {
//	btt, ok := c.Type().(*types.Basic)
//	if !ok {
//		panic("constant should be always of basic type")
//	}
//	isTyped := btt.Info()&types.IsUntyped == 0
//
//	var b strings.Builder
//
//	b.WriteString(c.Name())
//	if isTyped {
//		b.WriteByte(' ')
//		b.WriteString(c.Type().String())
//	}
//	b.WriteString(" = ")
//	b.WriteString(c.Val().String())
//
//	return b.String()
//}

type Variable struct {
	*types.Var

	Type    *TypeInfo
	Pkg     *Package // some duplicated with types.Var.Pkg()
	AstDecl *ast.GenDecl
	AstSpec *ast.ValueSpec
}

func (v *Variable) Position() token.Position {
	for _, n := range v.AstSpec.Names {
		if n.Name == v.Name() {
			return v.Pkg.PPkg.Fset.PositionFor(n.Pos(), false)
		}
	}
	panic("should not")
}

func (v *Variable) Documentation() string {
	doc := v.AstSpec.Doc.Text()
	if doc == "" {
		doc = v.AstDecl.Doc.Text()
	}
	return doc
}

func (v *Variable) Comment() string {
	return v.AstSpec.Comment.Text()
}

func (v *Variable) Package() *Package {
	return v.Pkg
}

func (v *Variable) Exported() bool {
	if v.Pkg.Path() == "builtin" {
		return !token.IsExported(v.Name())
	}
	return v.Var.Exported()
}

func (v *Variable) TType() types.Type {
	return v.Var.Type()
}

func (v *Variable) TypeInfo(d *CodeAnalyzer) *TypeInfo {
	if v.Type == nil {
		v.Type = d.RegisterType(v.TType())
	}
	return v.Type
}

//func (v *Variable) IndexString() string {
//	var b strings.Builder
//
//	b.WriteString(v.Name())
//	b.WriteByte(' ')
//	b.WriteString(v.Type().String())
//
//	s := b.String()
//	println(s)
//	return s
//}

type Function struct {
	*types.Func
	*types.Builtin // for builtin functions

	// Builtin, Variadic.
	attributes Attribute

	// ToDo: maintain parameter and result TypeInfos, for performance.

	// ToDo
	fSigIndex uint32 // as package function
	mSigIndex uint32 // as method, (ToDo: make 0 as invalid function index)

	Type    *TypeInfo
	Pkg     *Package // some duplicated with types.Func.Pkg(), except builtin functions
	AstDecl *ast.FuncDecl
}

func (f *Function) Name() string {
	if f.Func != nil {
		return f.Func.Name()
	}
	return f.Builtin.Name()
}

func (f *Function) Exported() bool {
	if f.Builtin != nil {
		return true
	}
	if f.Pkg.Path() == "builtin" {
		return !token.IsExported(f.Name())
	}
	return f.Func.Exported()
}

func (f *Function) Position() token.Position {
	return f.Pkg.PPkg.Fset.PositionFor(f.AstDecl.Name.Pos(), false)
}

func (f *Function) Documentation() string {
	// ToDo: html escape
	return f.AstDecl.Doc.Text()
}

func (f *Function) Comment() string {
	return ""
}

func (f *Function) Package() *Package {
	return f.Pkg
}

func (f *Function) TType() types.Type {
	if f.Func != nil {
		return f.Func.Type()
	}
	return f.Builtin.Type()
}

func (f *Function) TypeInfo(d *CodeAnalyzer) *TypeInfo {
	if f.Type == nil {
		f.Type = d.RegisterType(f.TType())
	}
	return f.Type
}

func (f *Function) IsMethod() bool {
	return f.Func != nil && f.Func.Type().(*types.Signature).Recv() != nil
}

func (f *Function) String() string {
	if f.Func != nil {
		return f.Func.String()
	}
	return f.Builtin.String()
}

//func (f *Function) IndexString() string {
//	var b strings.Builder
//	b.WriteString(f.Name())
//	b.WriteByte(' ')
//	WriteType(&b, f.AstDecl.Type, f.Pkg.PPkg.TypesInfo, true)
//	return b.String()
//}

// Please make sure the Funciton is a method when calling this method.
func (f *Function) ReceiverTypeName() (paramField *ast.Field, typeIdent *ast.Ident, isStar bool) {
	if f.AstDecl.Recv == nil {
		panic("should not")
	}
	if len(f.AstDecl.Recv.List) != 1 {
		panic("should not")
	}

	paramField = f.AstDecl.Recv.List[0]
	switch expr := paramField.Type.(type) {
	default:
		panic("should not")
	case *ast.Ident:
		typeIdent = expr
		isStar = false
		return
	case *ast.StarExpr:
		tid, ok := expr.X.(*ast.Ident)
		if !ok {
			panic("should not")
		}
		typeIdent = tid
		isStar = true
		return
	}
}

// ToDo: not use types.NewMethodSet or typesutil.MethodSet().
//       Implement it from scratch instead.
//type Method struct {
//	*types.Func // receiver is ignored
//
//	SignatureIndex uint32
//
//	PointerReceiverOnly bool
//
//	// The embedded type names in full form.
//	// Nil means this method is not obtained through embedding.
//	SelectorChain []Embedded
//
//	astFunc *ast.FuncDecl
//}

type MethodSignature struct {
	Name string // must be an identifier other than "_"
	Pkg  string // the import path, for unepxorted method names only

	//InOutTypes []int32 // global type indexes
	InOutTypes string

	NumInOutAndVariadic int
}

//// The lower bits of each Embedded is an index to the global TypeName table.
//// The global TypeName table comtains all type aliases and defined types.
//// The highest bit indicates whether or not the embedding for is *T or not.
//type Embedded uint32
//
//type Field struct {
//	*types.Var
//
//	// The info is contained in the above types.Var field.
//	//Owner *TypeInfo // must be a (non-defined) struct type
//
//	// The embedded type names in full form.
//	// Nil means this is a non-embedded field.
//	SelectorChain []Embedded
//
//	astList  *ast.FieldList
//	astField *ast.Field
//}
//
//type Method struct {
//	*types.Func // object denoted by x.f
//
//	SelectorChain []Embedded
//
//	astFunc *ast.FuncDecl
//}

type EmbedMode uint8

const (
	EmbedMode_None EmbedMode = iota
	EmbedMode_Direct
	EmbedMode_Indirect
)

type Field struct {
	astStruct    *ast.StructType
	AstField     *ast.Field
	astInterface *ast.InterfaceType // for embedding interface in interface

	Pkg  *Package // nil for exported
	Name string
	Type *TypeInfo

	Tag  string
	Mode EmbedMode
}

func (fld *Field) Position() token.Position {
	return fld.Pkg.PPkg.Fset.PositionFor(fld.AstField.Pos(), false)
}

type Method struct {
	AstFunc      *ast.FuncDecl      // for concrete methods
	astInterface *ast.InterfaceType // for interface methods
	AstField     *ast.Field         // for interface methods

	Pkg  *Package // nil for exported
	Name string
	Type *TypeInfo // ToDo: use custom struct including PointerRecv instead.

	PointerRecv bool // duplicated info, for faster access
}

func (mthd *Method) Position() token.Position {
	return mthd.Pkg.PPkg.Fset.PositionFor(mthd.AstFunc.Pos(), false)
}

type EmbeddedField struct {
	*Field
	Prev *EmbeddedField
}

type SelectorCond uint8

const (
	SelectorCond_Normal SelectorCond = iota
	SelectorCond_Hidden
)

type Selector struct {
	Id string

	// One and only one of the two is nil.
	*Field
	*Method

	// EmbeddedField is nil means this is not an promoted selector.
	//EmbeddedFields []*Field

	EmbeddingChain *EmbeddedField // in the inverse order
	Depth          uint16         // the chain length
	Indirect       bool           // whether the chain contains indirects or not

	// colliding or shadowed susposed promoted selector?
	//shadowed bool // used in collecting phase.
	cond SelectorCond
}

func (s *Selector) Reset() {
	*s = Selector{}
}

func (s *Selector) Position() token.Position {
	if s.Field != nil {
		return s.Field.Pkg.PPkg.Fset.PositionFor(s.Field.AstField.Pos(), false)
	} else if s.Method.AstFunc != nil { // method declaration
		return s.Method.Pkg.PPkg.Fset.PositionFor(s.Method.AstFunc.Pos(), false)
	} else { // if s.Method.AstField != nil //initerface method specification
		return s.Method.Pkg.PPkg.Fset.PositionFor(s.Method.AstField.Pos(), false)
	}
}

func (s *Selector) Name() string {
	if s.Field != nil {
		return s.Field.Name
	} else {
		return s.Method.Name
	}
}

func (s *Selector) Pkg() *Package {
	if s.Field != nil {
		return s.Field.Pkg
	} else {
		return s.Method.Pkg
	}
}

//func (s *Selector) Depth() int {
//	return len(s.EmbeddedFields)
//}

func (s *Selector) PointerReceiverOnly() bool {
	if s.Method == nil {
		panic("not a method selector")
	}

	return !s.Indirect && s.Method.PointerRecv
}

func (s *Selector) String() string {
	return EmbededFieldsPath(s.EmbeddingChain, nil, s.Name(), s.Field != nil)
}

func EmbededFieldsPath(embedding *EmbeddedField, b *strings.Builder, selName string, isField bool) (r string) {
	if embedding == nil {
		if isField {
			return "[field] " + selName
		} else {
			return "[method] " + selName
		}
	}
	if b == nil {
		b = &strings.Builder{}
		if isField {
			b.WriteString("[field] ")
		} else {
			b.WriteString("[method] ")
		}
		defer func() {
			b.WriteString(selName)
			r = b.String()
		}()
	}
	if p := embedding.Prev; p != nil {
		EmbededFieldsPath(p, b, "", isField)
	}
	if embedding.Field.Mode == EmbedMode_Indirect {
		b.WriteByte('*')
	}
	b.WriteString(embedding.Field.Name)
	b.WriteByte('.')
	return
}

func PrintSelectors(title string, selectors []*Selector) {
	log.Printf("%s (%d)\n", title, len(selectors))
	for _, sel := range selectors {
		log.Println("  ", sel)
	}
}

// ToDo: use go/doc package
