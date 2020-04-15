package server

import (
	"golang.org/x/text/language"
	//"golang.org/x/text/language/display"

	theme "go101.org/gold/server/themes"
	translation "go101.org/gold/server/translations"
)

var (
	allThemes       []Theme
	allTranslations []Translation
	langMatcher     language.Matcher
)

// All themes and translations must be registered at init phase,
// so that no syncrhomization is needed.
func init() {
	allThemes = make([]Theme, 0, 2)
	registerTheme(&theme.Light{})

	langTags := make([]language.Tag, 0, 8)
	allTranslations = make([]Translation, 0, cap(langTags))
	registerTranslation(&translation.English{}, &langTags)

	langMatcher = language.NewMatcher(langTags)
}

type Theme interface {
	Name() string
	CSS() string
}

type Translation interface {
	Name() string
	LangTag() string

	// overview page
	Text_Overview() string
	Text_PackageList(num int) string
	Text_AllStats(stats map[string]int) string               // to use
	Text_Modules() string                                    // to use
	Text_BelongingModule() string                            // to use
	Text_RequireStat(numRequires, numRequiredBys int) string // to use

	// package details page
	Text_Package() string
	Text_BelongingPackage() string // also used in source code page
	Text_ImportPath() string
	Text_DependencyRelations() string // also used in package dependencoes page
	Text_ImportStat(numImports, numImportedBys int, depPageURL string) string
	Text_InvolvedFiles(num int) string
	Text_ExportedValues(num int) string
	Text_ExportedTypeNames(num int) string
	Text_UnexportedTypeNames(num int) string // to use

	// package details page: type details
	Text_Fields(num int) string
	Text_Methods(num int) string
	Text_ImplementedBy(num int) string
	Text_Implements(num int) string
	Text_AsOutputsOf(num int) string
	Text_AsInputsOf(num int) string
	Text_AsTypesOf(num int) string
	Text_References(num int) string

	// package dependencies page
	Text_Imports() string
	Text_ImportedBy() string

	// source code page
	Text_SourceCode() string
	Text_SourceFilePath() string
	Text_GeneratedFrom() string
}

func registerTheme(theme Theme) {
	allThemes = append(allThemes, theme)
}

func registerTranslation(tr Translation, tags *[]language.Tag) {
	allTranslations = append(allTranslations, tr)
	t := language.Make(tr.LangTag())
	*tags = append(*tags, t)
}

func themeByName(name string) Theme {
	theme := allThemes[0]
	for _, t := range allThemes[1:] {
		if t.Name() == name {
			theme = t
			break
		}
	}
	return theme
}

func translationByName(name string) Translation {
	trans := allTranslations[0]
	for _, tr := range allTranslations[1:] {
		if tr.Name() == name {
			trans = tr
			break
		}
	}
	return trans
}

func translationByTag(tag string) Translation {
	trans := allTranslations[0]
	// ToDo: langMatcher
	return trans
}

func (ds *docServer) changeSettings(themeName, langTag string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	// ToDo:
	ds.currentTheme = allThemes[0]
	ds.currentTranslation = allTranslations[0]
}