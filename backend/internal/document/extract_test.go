package document

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildZip 在内存里构造一个 zip（docx/xlsx 本质都是 zip）。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractDocx(t *testing.T) {
	doc := buildZip(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>你好</w:t></w:r><w:r><w:t>世界</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>第二段</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	got, err := ExtractText("报告.DOCX", doc)
	if err != nil {
		t.Fatal(err)
	}
	want := "你好世界\n第二段"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractDocxBroken(t *testing.T) {
	if _, err := ExtractText("a.docx", []byte("not a zip")); err == nil {
		t.Fatal("expected error for broken docx")
	}
}

func TestExtractXlsx(t *testing.T) {
	wb := buildZip(t, map[string]string{
		"xl/workbook.xml": `<workbook><sheets><sheet name="表1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships>` +
			`<Relationship Id="rId1" Target="worksheets/sheet1.xml"/>` +
			`</Relationships>`,
		"xl/sharedStrings.xml": `<sst><si><t>姓名</t></si><si><t>年龄</t></si><si><t>张三</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>30</v></c></row>` +
			`<row r="3"><c r="A3"/><c r="B3"/></row>` +
			`</sheetData></worksheet>`,
	})
	got, err := ExtractText("data.xlsx", wb)
	if err != nil {
		t.Fatal(err)
	}
	want := "工作表：表1\n姓名,年龄\n张三,30"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractLegacyUnsupported(t *testing.T) {
	_, err := ExtractText("old.doc", []byte("xx"))
	if err != ErrUnsupportedFormat {
		t.Fatalf("want ErrUnsupportedFormat, got %v", err)
	}
	if _, err := ExtractText("old.xls", []byte("xx")); err != ErrUnsupportedFormat {
		t.Fatalf("want ErrUnsupportedFormat, got %v", err)
	}
}

func TestIsOfficeName(t *testing.T) {
	for _, yes := range []string{"a.docx", "b.XLSX", "c.doc", "d.xlsb"} {
		if !IsOfficeName(yes) {
			t.Fatalf("%s should be office", yes)
		}
	}
	for _, no := range []string{"a.txt", "b.pdf", "docx"} {
		if IsOfficeName(no) {
			t.Fatalf("%s should not be office", no)
		}
	}
}
