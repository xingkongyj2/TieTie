// Package document 从 Office 文档中提取纯文本（对应 server/document-text.mjs）。
//
// 当前支持 .docx / .xlsx / .xlsm（纯标准库 zip+xml 实现）；
// 旧版二进制格式 .doc / .xls / .xlsb 返回 ErrUnsupportedFormat。
package document

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnsupportedFormat 表示旧版二进制 Office 格式，Go 版暂不支持解析。
var ErrUnsupportedFormat = errors.New("unsupported legacy office format")

var officeExtRe = regexp.MustCompile(`(?i)\.(xlsx|xls|xlsm|xlsb|docx|doc)$`)

// IsOfficeName 判断文件名是否为 Office 文档扩展名（对应 OFFICE_EXTENSIONS）。
func IsOfficeName(name string) bool { return officeExtRe.MatchString(name) }

// ExtractText 按扩展名提取文档文本，返回错误时调用方应转换为 4xx 业务错误。
func ExtractText(name string, data []byte) (string, error) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".docx"):
		return extractDocx(data)
	case strings.HasSuffix(lower, ".xlsx"), strings.HasSuffix(lower, ".xlsm"):
		return extractXlsx(data)
	case strings.HasSuffix(lower, ".doc"), strings.HasSuffix(lower, ".xls"), strings.HasSuffix(lower, ".xlsb"):
		return "", ErrUnsupportedFormat
	default:
		return "", errors.New("不支持的 Office 文件格式。")
	}
}

// ---- docx ----

func extractDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	entry := zipFind(zr, "word/document.xml")
	if entry == nil {
		return "", errors.New("docx 缺少 word/document.xml")
	}
	rc, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var sb strings.Builder
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t": // 文本 run
				text, err := readElementText(dec, "t")
				if err != nil {
					return "", err
				}
				sb.WriteString(text)
			case "tab":
				sb.WriteString("\t")
			case "br", "cr":
				sb.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "p" { // 段落结束换行
				sb.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// readElementText 收集当前元素内所有 CharData，直到匹配的结束标签。
func readElementText(dec *xml.Decoder, local string) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return sb.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.EndElement:
			if t.Name.Local == local {
				return sb.String(), nil
			}
		}
	}
}

// ---- xlsx ----

func extractXlsx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	shared, err := readSharedStrings(zr)
	if err != nil {
		return "", err
	}
	sheets, err := readWorkbookSheets(zr)
	if err != nil {
		return "", err
	}

	var sheetTexts []string
	for _, sh := range sheets {
		entry := zipFind(zr, sh.target)
		if entry == nil {
			continue
		}
		rows, formulas, err := readSheet(entry, shared)
		if err != nil {
			return "", err
		}
		var csvBuf bytes.Buffer
		cw := csv.NewWriter(&csvBuf)
		for _, row := range rows {
			if isEmptyRow(row) {
				continue
			}
			if err := cw.Write(row); err != nil {
				return "", err
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return "", err
		}
		parts := []string{"工作表：" + sh.name, strings.TrimSpace(csvBuf.String())}
		if len(formulas) > 0 {
			parts = append(parts, "未计算公式：\n"+strings.Join(formulas, "\n"))
		}
		text := strings.TrimSpace(joinNonEmpty(parts, "\n"))
		if text != "" && strings.Contains(text, "\n") {
			sheetTexts = append(sheetTexts, text)
		}
	}
	return strings.Join(sheetTexts, "\n\n"), nil
}

type sheetRef struct {
	name   string
	target string // zip 内路径，如 xl/worksheets/sheet1.xml
}

func readSharedStrings(zr *zip.Reader) ([]string, error) {
	entry := zipFind(zr, "xl/sharedStrings.xml")
	if entry == nil {
		return nil, nil
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var (
		out     []string
		inSI    bool
		current strings.Builder
	)
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "si" {
				inSI = true
				current.Reset()
			} else if inSI && t.Name.Local == "t" {
				text, err := readElementText(dec, "t")
				if err != nil {
					return nil, err
				}
				current.WriteString(text)
			}
		case xml.EndElement:
			if t.Name.Local == "si" && inSI {
				out = append(out, current.String())
				inSI = false
			}
		}
	}
	return out, nil
}

func readWorkbookSheets(zr *zip.Reader) ([]sheetRef, error) {
	wb := zipFind(zr, "xl/workbook.xml")
	if wb == nil {
		return nil, errors.New("xlsx 缺少 xl/workbook.xml")
	}
	rc, err := wb.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	type pending struct{ name, rid string }
	var pendings []pending
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			var p pending
			for _, attr := range se.Attr {
				switch attr.Name.Local {
				case "name":
					p.name = attr.Value
				case "id":
					p.rid = attr.Value
				}
			}
			pendings = append(pendings, p)
		}
	}

	// rId → 目标路径
	targets := map[string]string{}
	if rels := zipFind(zr, "xl/_rels/workbook.xml.rels"); rels != nil {
		rc2, err := rels.Open()
		if err != nil {
			return nil, err
		}
		defer rc2.Close()
		dec2 := xml.NewDecoder(rc2)
		for {
			tok, err := dec2.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
				var id, target string
				for _, attr := range se.Attr {
					switch attr.Name.Local {
					case "Id":
						id = attr.Value
					case "Target":
						target = attr.Value
					}
				}
				if id != "" && target != "" {
					targets[id] = resolveSheetTarget(target)
				}
			}
		}
	}

	var sheets []sheetRef
	for _, p := range pendings {
		if t, ok := targets[p.rid]; ok {
			sheets = append(sheets, sheetRef{name: p.name, target: t})
		}
	}
	return sheets, nil
}

// resolveSheetTarget 把 rels 里的 Target 归一化成 zip 内绝对路径。
func resolveSheetTarget(target string) string {
	target = strings.TrimPrefix(target, "/")
	if strings.HasPrefix(target, "xl/") {
		return target
	}
	return "xl/" + target
}

// readSheet 解析单个工作表，返回 CSV 行与未计算公式列表。
func readSheet(entry *zip.File, shared []string) ([][]string, []string, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()

	var (
		rows     [][]string
		formulas []string
		curRow   []string
		nextCol  int

		inCell     bool
		cellType   string
		cellRef    string
		cellVal    string
		cellForm   string
		cellInl    string
		collecting string // "v" | "f" | "t"
		buf        strings.Builder
	)
	flushCell := func() {
		value := ""
		switch cellType {
		case "s":
			if idx, err := strconv.Atoi(cellVal); err == nil && idx >= 0 && idx < len(shared) {
				value = shared[idx]
			}
		case "inlineStr":
			value = cellInl
		default: // 数字、公式结果(t="str")等
			value = cellVal
		}
		col := nextCol
		if cellRef != "" {
			col = colFromRef(cellRef, nextCol)
		}
		for len(curRow) <= col {
			curRow = append(curRow, "")
		}
		curRow[col] = value
		if col >= nextCol {
			nextCol = col + 1
		}
		if value == "" && cellForm != "" {
			ref := cellRef
			if ref == "" {
				ref = fmt.Sprintf("col%d", col)
			}
			formulas = append(formulas, ref+": ="+cellForm)
		}
	}

	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				curRow = nil
				nextCol = 0
			case "c":
				inCell = true
				cellType, cellRef, cellVal, cellForm, cellInl = "", "", "", "", ""
				for _, attr := range t.Attr {
					switch attr.Name.Local {
					case "t":
						cellType = attr.Value
					case "r":
						cellRef = attr.Value
					}
				}
			case "v", "f":
				if inCell {
					collecting = t.Name.Local
					buf.Reset()
				}
			case "t":
				if inCell && cellType == "inlineStr" {
					collecting = "t"
					buf.Reset()
				}
			}
		case xml.CharData:
			if collecting != "" {
				buf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "f", "t":
				if collecting == t.Name.Local {
					switch collecting {
					case "v":
						cellVal = buf.String()
					case "f":
						cellForm = buf.String()
					case "t":
						cellInl += buf.String()
					}
					collecting = ""
				}
			case "c":
				if inCell {
					flushCell()
					inCell = false
				}
			case "row":
				rows = append(rows, curRow)
				curRow = nil
			}
		}
	}
	return rows, formulas, nil
}

// colFromRef 从 "BC12" 形式的单元格引用解析列号（0 起）。
func colFromRef(ref string, fallback int) int {
	col := 0
	letters := 0
	for _, ch := range strings.ToUpper(ref) {
		if ch < 'A' || ch > 'Z' {
			break
		}
		col = col*26 + int(ch-'A') + 1
		letters++
	}
	if letters == 0 {
		return fallback
	}
	return col - 1
}

func isEmptyRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// zipFind 按路径查找 zip 条目（大小写不敏感兜底）。
func zipFind(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	lower := strings.ToLower(name)
	for _, f := range zr.File {
		if strings.ToLower(f.Name) == lower {
			return f
		}
	}
	return nil
}
