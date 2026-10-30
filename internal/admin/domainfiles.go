package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"gopkg.in/yaml.v3"
)

// domainFile is one YAML file of the domains directory and the hosts it
// defines. hosts is empty for a file that does not parse or names no host.
type domainFile struct {
	path    string
	hosts   []string
	include bool // pulled in by an include: pattern, not found in domains.d
}

// scanDomainFiles lists the .yaml/.yml files of dir and the hosts each defines,
// read with the loader's own rules so the answer matches what a restart loads.
func scanDomainFiles(dir string) []domainFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []domainFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		f := domainFile{path: filepath.Join(dir, name)}
		if doms, err := config.LoadDomainFile(f.path); err == nil {
			for _, d := range doms {
				f.hosts = append(f.hosts, d.Host)
			}
		}
		files = append(files, f)
	}
	return files
}

// scanDomainSources is scanDomainFiles plus the files the main config pulls in
// with `include:` patterns (resolved against the config's directory, as Load
// does). An included domain lives in memory like any other, so persistConfig
// wrote it into domains.d as well and the next start read the host twice
// ("duplicate host"); it has to know those files to leave them the owner.
func scanDomainSources(domainsDir, baseDir string, includes []string) []domainFile {
	files := scanDomainFiles(domainsDir)
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.path] = true
	}
	for _, pat := range includes {
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(baseDir, pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			f := domainFile{path: m, include: true}
			if doms, err := config.LoadDomainFile(m); err == nil {
				for _, d := range doms {
					f.hosts = append(f.hosts, d.Host)
				}
			}
			files = append(files, f)
		}
	}
	return files
}

// replaceDomainInListFile stores dom as the entry for the idx-th domain of an
// included list file, unless the file already holds that same domain. The
// other entries, their comments and their ${VAR} references stay as written.
func replaceDomainInListFile(path string, idx, want int, dom config.Domain) error {
	loaded, err := config.LoadDomainFile(path)
	if err != nil {
		return err
	}
	if len(loaded) != want || idx >= len(loaded) {
		return fmt.Errorf("list does not line up with the loaded domains")
	}
	if reflect.DeepEqual(loaded[idx], dom) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("not a domains list")
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "domains" || top.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		seq := top.Content[i+1]
		if len(seq.Content) != want {
			return fmt.Errorf("list does not line up with the loaded domains")
		}
		// The ${VAR} references and the comment of this entry stay: only the
		// values the panel changed are replaced.
		newBytes, err := yaml.Marshal(&dom)
		if err != nil {
			return err
		}
		if oldBytes, err := yaml.Marshal(seq.Content[idx]); err == nil {
			newBytes = config.PreserveEnvReferences(oldBytes, newBytes)
		}
		var entryDoc yaml.Node
		if err := yaml.Unmarshal(newBytes, &entryDoc); err != nil || entryDoc.Kind != yaml.DocumentNode || len(entryDoc.Content) == 0 {
			return fmt.Errorf("could not re-encode domain %s", dom.Host)
		}
		entry := entryDoc.Content[0]
		entry.HeadComment = seq.Content[idx].HeadComment
		seq.Content[idx] = entry
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return err
		}
		return atomicWriteFile(path, out, 0600)
	}
	return fmt.Errorf("no domains list")
}

// supersedeDomainFiles retires a domains.d file whose every domain is now
// stored in a file of its own. persistConfig writes <host>.yaml for each
// domain, so a domain that came from site1.yaml, a .yml file or a list file
// would otherwise be defined twice and the next start would refuse the
// configuration ("duplicate host"). The file is renamed to <name>.superseded
// (no longer loaded, still on disk) rather than deleted, and only when all of
// its hosts are in the live configuration: a file with a host that was not
// loaded is left alone, which is what the v0.5.6 change to never delete
// anything protects.
func (s *Server) supersedeDomainFiles(files []domainFile, written map[string]bool, live map[string]bool) {
	for _, f := range files {
		// An included file is never renamed: an include pattern such as
		// sites/* would still match "<name>.superseded" and load it again.
		if f.include || written[f.path] || len(f.hosts) == 0 {
			continue
		}
		all := true
		for _, h := range f.hosts {
			if !live[strings.ToLower(h)] {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		if err := os.Rename(f.path, f.path+".superseded"); err != nil {
			s.logger.Warn("could not retire duplicate domain file", "path", f.path, "error", err)
			continue
		}
		s.logger.Info("retired domain file now stored under its host name", "path", f.path, "hosts", f.hosts)
	}
}

// removeHostFromDomainFiles removes host from every domains.d file that still
// defines it under another name: a single-domain file is deleted, a file that
// lists several domains has just that entry cut out (comments and ${VAR}
// references of the others stay). removeDomainFile only looked for
// <host>.yaml/.yml, so such a domain came back at the next start.
func (s *Server) removeHostFromDomainFiles(dir, baseDir string, includes []string, host string) {
	for _, f := range scanDomainSources(dir, baseDir, includes) {
		idx := -1
		for i, h := range f.hosts {
			if strings.EqualFold(h, host) {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		if len(f.hosts) == 1 {
			if err := os.Remove(f.path); err == nil {
				s.logger.Info("removed domain file", "path", f.path)
			}
			continue
		}
		if err := cutDomainFromListFile(f.path, idx, len(f.hosts)); err != nil {
			s.logger.Warn("could not remove domain from list file", "path", f.path, "host", host, "error", err)
			continue
		}
		s.logger.Info("removed domain from list file", "path", f.path, "host", host)
	}
}

// cutDomainFromListFile drops the idx-th entry of the top-level domains list.
func cutDomainFromListFile(path string, idx, want int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("not a domains list")
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "domains" || top.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		seq := top.Content[i+1]
		if len(seq.Content) != want || idx >= len(seq.Content) {
			return fmt.Errorf("list does not line up with the loaded domains")
		}
		seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return err
		}
		return atomicWriteFile(path, out, 0600)
	}
	return fmt.Errorf("no domains list")
}
