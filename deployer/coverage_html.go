// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// With changes for evm-deploy-contract Solidity coverage.
package deployer

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	log "github.com/xlab/suplog"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/tools/cover"
)

// htmlOutput reads the profile data from profile and generates an HTML
// coverage report, writing it to outfile. If outfile is empty,
// it writes the report to a temporary file and opens it in a web browser.
func (c *coverageDataCollector) htmlOutput(profiles map[string]*cover.Profile, out io.Writer) (err error) {
	var d templateData

	var shouldStartBrowser bool
	var tempFile *os.File
	if out == nil || out == (*os.File)(nil) {
		shouldStartBrowser = true

		tempFile, err = ioutil.TempFile("", "*-sol-coverprofile.html")
		if err != nil {
			return err
		}

		out = tempFile
	}

	mergedProfiles, err := mergeSortProfiles(c.coverageMode, profiles)
	if err != nil {
		return err
	}

	for _, profile := range mergedProfiles {
		if profile.Mode == "set" {
			d.Set = true
		}

		src, err := ioutil.ReadFile(profile.FileName)
		if err != nil {
			return fmt.Errorf("can't read %q: %v", profile.FileName, err)
		}
		profile.FileName = limitPath(profile.FileName, reportPathSegments)

		var buf bytes.Buffer
		err = htmlGen(&buf, src, profile.Boundaries(src))
		if err != nil {
			return err
		}

		d.Files = append(d.Files, &templateFile{
			Name:     profile.FileName,
			Body:     template.HTML(buf.String()),
			Coverage: percentCovered(profile),
		})
	}

	err = htmlTemplate.Execute(out, d)
	if err != nil {
		return err
	}

	if shouldStartBrowser {
		if !startBrowser("file://" + tempFile.Name()) {
			log.Warningln(os.Stderr, "HTML output written to %s\n", tempFile.Name())
		}
	}

	return nil
}

const reportPathSegments = 3

func limitPath(path string, n int) string {
	if n <= 0 {
		return path
	}

	pathParts := strings.Split(path, string(filepath.Separator))
	if len(pathParts) > n {
		pathParts = pathParts[len(pathParts)-n:]
	}

	return filepath.Join(pathParts...)
}

func mergeSortProfiles(mode CoverageMode, unmergedProfiles map[string]*cover.Profile) ([]*cover.Profile, error) {
	for _, p := range unmergedProfiles {
		sort.Sort(blocksByStart(p.Blocks))
		// Merge samples from the same location.
		j := 1
		for i := 1; i < len(p.Blocks); i++ {
			b := p.Blocks[i]
			last := p.Blocks[j-1]
			if b.StartLine == last.StartLine &&
				b.StartCol == last.StartCol &&
				b.EndLine == last.EndLine &&
				b.EndCol == last.EndCol {
				if b.NumStmt != last.NumStmt {
					return nil, fmt.Errorf("inconsistent NumStmt: changed from %d to %d", last.NumStmt, b.NumStmt)
				}
				if mode == "set" {
					p.Blocks[j-1].Count |= b.Count
				} else {
					p.Blocks[j-1].Count += b.Count
				}
				continue
			}
			p.Blocks[j] = b
			j++
		}
		p.Blocks = p.Blocks[:j]
	}
	// Generate a sorted slice.
	profiles := make([]*cover.Profile, 0, len(unmergedProfiles))
	for _, profile := range unmergedProfiles {
		profiles = append(profiles, profile)
	}
	sort.Stable(sort.Reverse(byScore(profiles)))

	return profiles, nil
}

type byScore []*cover.Profile

func (p byScore) Len() int { return len(p) }
func (p byScore) Less(i, j int) bool {
	s1 := percentCovered(p[i])
	s2 := percentCovered(p[j])
	if s1 == s2 {
		return p[i].FileName < p[j].FileName
	}

	return s1 < s2
}
func (p byScore) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

type blocksByStart []cover.ProfileBlock

func (b blocksByStart) Len() int      { return len(b) }
func (b blocksByStart) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b blocksByStart) Less(i, j int) bool {
	bi, bj := b[i], b[j]
	return bi.StartLine < bj.StartLine || bi.StartLine == bj.StartLine && bi.StartCol < bj.StartCol
}

// percentCovered returns, as a percentage, the fraction of the statements in
// the profile covered by the test run.
// In effect, it reports the coverage of a given source file.
func percentCovered(p *cover.Profile) float64 {
	var total, covered int64
	for _, b := range p.Blocks {
		total += int64(b.NumStmt)
		if b.Count > 0 {
			covered += int64(b.NumStmt)
		}
	}
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total) * 100
}

// htmlGen generates an HTML coverage report with the provided filename,
// source code, and tokens, and writes it to the given Writer.
func htmlGen(w io.Writer, src []byte, boundaries []cover.Boundary) error {
	dst := bufio.NewWriter(w)
	for i := range src {
		for len(boundaries) > 0 && boundaries[0].Offset == i {
			b := boundaries[0]
			if b.Start {
				n := 0
				if b.Count > 0 {
					n = int(math.Floor(b.Norm*9)) + 1
				}
				fmt.Fprintf(dst, `<span class="cov%v" title="%v">`, n, b.Count)
			} else {
				dst.WriteString("</span>")
			}
			boundaries = boundaries[1:]
		}
		switch b := src[i]; b {
		case '>':
			dst.WriteString("&gt;")
		case '<':
			dst.WriteString("&lt;")
		case '&':
			dst.WriteString("&amp;")
		case '\t':
			dst.WriteString("        ")
		default:
			dst.WriteByte(b)
		}
	}
	return dst.Flush()
}

// startBrowser tries to open the URL in a browser
// and reports whether it succeeds.
func startBrowser(url string) bool {
	// try to start the browser
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"cmd", "/c", "start"}
	default:
		args = []string{"xdg-open"}
	}
	cmd := exec.Command(args[0], append(args[1:], url)...)
	return cmd.Start() == nil
}

// rgb returns an rgb value for the specified coverage value
// between 0 (no coverage) and 10 (max coverage).
func rgb(n int) string {
	if n == 0 {
		return "rgb(192, 0, 0)" // Red
	}
	// Gradient from gray to green.
	r := 128 - 12*(n-1)
	g := 128 + 12*(n-1)
	b := 128 + 3*(n-1)
	return fmt.Sprintf("rgb(%v, %v, %v)", r, g, b)
}

// colors generates the CSS rules for coverage colors.
func colors() template.CSS {
	var buf bytes.Buffer
	for i := 0; i < 11; i++ {
		fmt.Fprintf(&buf, ".cov%v { color: %v }\n", i, rgb(i))
	}
	return template.CSS(buf.String())
}

var htmlTemplate = template.Must(template.New("html").Funcs(template.FuncMap{
	"colors": colors,
}).Parse(tmplHTML))

type templateData struct {
	Files []*templateFile
	Set   bool
}

type templateFile struct {
	Name     string
	Body     template.HTML
	Coverage float64
}

const tmplHTML = `
<!DOCTYPE html>
<html>
	<head>
		<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
		<style>
			body {
				background: black;
				color: rgb(80, 80, 80);
			}
			body, pre, #legend span {
				font-family: Menlo, monospace;
				font-weight: bold;
			}
			#topbar {
				background: black;
				position: fixed;
				top: 0; left: 0; right: 0;
				height: 42px;
				border-bottom: 1px solid rgb(80, 80, 80);
			}
			#content {
				margin-top: 50px;
			}
			#nav, #legend {
				float: left;
				margin-left: 10px;
			}
			#legend {
				margin-top: 12px;
			}
			#nav {
				margin-top: 10px;
			}
			#legend span {
				margin: 0 5px;
			}
			{{colors}}
		</style>
	</head>
	<body>
		<div id="topbar">
			<div id="nav">
				<select id="files">
				{{range $i, $f := .Files}}
				<option value="file{{$i}}">{{$f.Name}} ({{printf "%.1f" $f.Coverage}}%)</option>
				{{end}}
				</select>
			</div>
			<div id="legend">
				<span>not tracked</span>
			{{if .Set}}
				<span class="cov0">not covered</span>
				<span class="cov8">covered</span>
			{{else}}
				<span class="cov0">no coverage</span>
				<span class="cov1">low coverage</span>
				<span class="cov2">*</span>
				<span class="cov3">*</span>
				<span class="cov4">*</span>
				<span class="cov5">*</span>
				<span class="cov6">*</span>
				<span class="cov7">*</span>
				<span class="cov8">*</span>
				<span class="cov9">*</span>
				<span class="cov10">high coverage</span>
			{{end}}
			</div>
		</div>
		<div id="content">
		{{range $i, $f := .Files}}
		<pre class="file" id="file{{$i}}" {{if $i}}style="display: none"{{end}}>{{$f.Body}}</pre>
		{{end}}
		</div>
	</body>
	<script>
	(function() {
		var files = document.getElementById('files');
		var visible = document.getElementById('file0');
		files.addEventListener('change', onChange, false);
		function onChange() {
			visible.style.display = 'none';
			visible = document.getElementById(files.value);
			visible.style.display = 'block';
			window.scrollTo(0, 0);
		}
	})();
	</script>
</html>
`