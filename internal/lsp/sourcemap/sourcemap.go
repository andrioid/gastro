package sourcemap

// SourceMap maps line numbers between a .gastro file and its virtual .go file.
// The mapping is a simple linear offset: lines in the frontmatter region shift
// by a constant amount between the two files.
type SourceMap struct {
	gastroFrontmatterStart  int // 1-indexed line where frontmatter starts in .gastro
	virtualFrontmatterStart int // 1-indexed line where frontmatter starts in virtual .go
}

// New creates a SourceMap. gastroStart is the line number where frontmatter
// content begins in the .gastro file. virtualStart is the line number where
// frontmatter content begins in the virtual .go file.
func New(gastroStart, virtualStart int) *SourceMap {
	return &SourceMap{
		gastroFrontmatterStart:  gastroStart,
		virtualFrontmatterStart: virtualStart,
	}
}

// VirtualToGastro converts a line number in the virtual .go file to the
// corresponding line in the .gastro file.
func (sm *SourceMap) VirtualToGastro(virtualLine int) int {
	offset := virtualLine - sm.virtualFrontmatterStart
	return sm.gastroFrontmatterStart + offset
}

// GastroToVirtual converts a line number in the .gastro file to the
// corresponding line in the virtual .go file.
func (sm *SourceMap) GastroToVirtual(gastroLine int) int {
	offset := gastroLine - sm.gastroFrontmatterStart
	return sm.virtualFrontmatterStart + offset
}
