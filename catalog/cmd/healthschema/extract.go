package main

import (
	"go/ast"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

// Schema is the top-level output.
type Schema struct {
	Enums   map[string]EnumSchema   `json:"enums"`
	Structs map[string]StructSchema `json:"structs"`
}

// EnumSchema describes a single int-backed enum type.
type EnumSchema struct {
	Description string      `json:"description"`
	Values      []EnumValue `json:"values"`
}

// EnumValue describes one constant in an enum.
type EnumValue struct {
	Name        string `json:"name"`
	JSON        string `json:"json,omitempty"`
	Description string `json:"description"`
}

// StructSchema describes a struct type.
type StructSchema struct {
	Description string        `json:"description"`
	Fields      []FieldSchema `json:"fields"`
}

// FieldSchema describes one field of a struct.
type FieldSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	JSON        string `json:"json,omitempty"`
	Description string `json:"description,omitempty"`
}

// extract walks all parsed files and builds the schema.
func extract(files []*ast.File) Schema {
	s := Schema{
		Enums:   make(map[string]EnumSchema),
		Structs: make(map[string]StructSchema),
	}

	// First pass: find int-backed types (enums) and struct types.
	intTypes := map[string]string{}    // type name -> doc comment
	structTypes := map[string]string{} // type name -> doc comment
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				doc := docText(ts.Doc, gd.Doc)
				switch t := ts.Type.(type) {
				case *ast.Ident:
					if t.Name == "int" {
						intTypes[ts.Name.Name] = doc
					}
				case *ast.StructType:
					structTypes[ts.Name.Name] = doc
					s.Structs[ts.Name.Name] = extractStruct(ts, t, doc)
				}
			}
		}
	}

	// Second pass: find name maps (var xNames = map[X]string{...}).
	nameMaps := map[string]map[string]string{} // enum type -> go name -> json name
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				varName := vs.Names[0].Name
				// Match pattern: var <type>Names = map[<Type>]string{...}
				// or <type>FromName. We want the <type>Names variant.
				if !strings.HasSuffix(varName, "Names") || strings.HasSuffix(varName, "FromName") {
					continue
				}
				cl, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				mt, ok := cl.Type.(*ast.MapType)
				if !ok {
					continue
				}
				keyIdent, ok := mt.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if _, isEnum := intTypes[keyIdent.Name]; !isEnum {
					continue
				}
				m := make(map[string]string)
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					goName := identName(kv.Key)
					jsonName := stringLitValue(kv.Value)
					if goName != "" && jsonName != "" {
						m[goName] = jsonName
					}
				}
				nameMaps[keyIdent.Name] = m
			}
		}
	}

	// Third pass: find const blocks with iota for each enum type.
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST || len(gd.Specs) == 0 {
				continue
			}
			// Check if first spec has iota and a known enum type.
			first := gd.Specs[0].(*ast.ValueSpec)
			if len(first.Names) == 0 {
				continue
			}
			enumType := ""
			if first.Type != nil {
				if ident, ok := first.Type.(*ast.Ident); ok {
					if _, isEnum := intTypes[ident.Name]; isEnum {
						enumType = ident.Name
					}
				}
			}
			if enumType == "" || !hasIota(first) {
				continue
			}

			jsonNames := nameMaps[enumType]
			var values []EnumValue
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if len(vs.Names) == 0 {
					continue
				}
				name := vs.Names[0].Name
				desc := constComment(vs)
				jsonName := ""
				if jsonNames != nil {
					jsonName = jsonNames[name]
				}
				values = append(values, EnumValue{
					Name:        name,
					JSON:        jsonName,
					Description: desc,
				})
			}
			s.Enums[enumType] = EnumSchema{
				Description: intTypes[enumType],
				Values:      values,
			}
		}
	}

	return s
}

// extractStruct builds a StructSchema from an AST struct type.
func extractStruct(ts *ast.TypeSpec, st *ast.StructType, doc string) StructSchema {
	fields := []FieldSchema{}
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			continue // embedded field
		}
		name := f.Names[0].Name
		if !ast.IsExported(name) {
			continue
		}
		typeName := typeString(f.Type)
		jsonTag := ""
		if f.Tag != nil {
			tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`"))
			if j, ok := tag.Lookup("json"); ok {
				jsonTag, _, _ = strings.Cut(j, ",")
			}
		}
		desc := ""
		if f.Doc != nil {
			desc = cleanComment(f.Doc.Text())
		} else if f.Comment != nil {
			desc = cleanComment(f.Comment.Text())
		}
		fields = append(fields, FieldSchema{
			Name:        name,
			Type:        typeName,
			JSON:        jsonTag,
			Description: desc,
		})
	}
	return StructSchema{Description: doc, Fields: fields}
}

// constComment extracts the description for a const value from either
// a preceding doc comment or an inline line comment.
func constComment(vs *ast.ValueSpec) string {
	if vs.Doc != nil && vs.Doc.Text() != "" {
		return cleanComment(vs.Doc.Text())
	}
	if vs.Comment != nil && vs.Comment.Text() != "" {
		return cleanComment(vs.Comment.Text())
	}
	return ""
}

// docText returns the doc comment text, preferring the spec-level doc
// over the declaration-level doc.
func docText(specDoc, declDoc *ast.CommentGroup) string {
	if specDoc != nil {
		return cleanComment(specDoc.Text())
	}
	if declDoc != nil {
		return cleanComment(declDoc.Text())
	}
	return ""
}

// hasIota checks if a ValueSpec contains iota in its values.
func hasIota(vs *ast.ValueSpec) bool {
	for _, v := range vs.Values {
		if ident, ok := v.(*ast.Ident); ok && ident.Name == "iota" {
			return true
		}
	}
	return false
}

// identName extracts the name from an *ast.Ident.
func identName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// stringLitValue extracts the string value from an *ast.BasicLit.
func stringLitValue(expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, _ := strconv.Unquote(lit.Value)
		return s
	}
	return ""
}

// typeString converts an AST type expression to a readable string.
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "any"
	default:
		return "unknown"
	}
}

// cleanComment trims whitespace and collapses a multi-line comment
// into a single line.
func cleanComment(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	var parts []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " ")
}
