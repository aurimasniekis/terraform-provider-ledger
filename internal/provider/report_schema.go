package provider

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The read-only data sources (ADR-0011) describe what Ledger reports as a small tree of nodes. One node produces the
// schema attribute, the framework type and the conversion of the decoded API value, so the three cannot drift apart.
// Money is always an object with an amount (an exact decimal string, never a number) and its own currency; the
// data sources never add, convert or compare amounts.

type nodeKind int

const (
	kStr nodeKind = iota
	kBool
	kInt
	kObj
	kList
	kJSON
)

type field struct {
	name string
	n    node
	key  string // the key in the API document, when it differs from the attribute name
}

type node struct {
	kind   nodeKind
	desc   string
	fields []field // kObj
	elem   *node   // kList
}

func str(d string) node             { return node{kind: kStr, desc: d} }
func boolean(d string) node         { return node{kind: kBool, desc: d} }
func integer(d string) node         { return node{kind: kInt, desc: d} }
func obj(d string, f ...field) node { return node{kind: kObj, desc: d, fields: f} }
func list(d string, elem node) node { return node{kind: kList, desc: d, elem: &elem} }

// rawJSON is a sub-document kept as the API's own JSON text: for parts whose shape is open (audit details) or too
// deep to be useful as attributes. Decode it with jsondecode().
func rawJSON(d string) node { return node{kind: kJSON, desc: d} }

func f(name string, n node) field { return field{name: name, n: n, key: name} }

// fk is a field whose attribute name differs from the API key (provider is a reserved attribute name in Terraform).
func fk(name, key string, n node) field { return field{name: name, n: n, key: key} }

// money is an exact amount with its currency. The amount is a decimal string in the form the API sent.
func money(d string) node {
	return obj(d,
		f("amount", str("Exact decimal amount as a string, in the scale Ledger stores it. Never a floating-point number.")),
		f("currency", str("ISO 4217 currency code of amount.")))
}

func (n node) attrType() attr.Type {
	switch n.kind {
	case kBool:
		return types.BoolType
	case kInt:
		return types.Int64Type
	case kObj:
		m := map[string]attr.Type{}
		for _, fl := range n.fields {
			m[fl.name] = fl.n.attrType()
		}
		return types.ObjectType{AttrTypes: m}
	case kList:
		return types.ListType{ElemType: n.elem.attrType()}
	default:
		return types.StringType
	}
}

func (n node) attribute() schema.Attribute {
	switch n.kind {
	case kBool:
		return schema.BoolAttribute{Computed: true, MarkdownDescription: n.desc}
	case kInt:
		return schema.Int64Attribute{Computed: true, MarkdownDescription: n.desc}
	case kObj:
		return schema.SingleNestedAttribute{Computed: true, MarkdownDescription: n.desc, Attributes: attributes(n.fields)}
	case kList:
		if n.elem.kind == kObj {
			return schema.ListNestedAttribute{Computed: true, MarkdownDescription: n.desc,
				NestedObject: schema.NestedAttributeObject{Attributes: attributes(n.elem.fields)}}
		}
		return schema.ListAttribute{Computed: true, MarkdownDescription: n.desc, ElementType: n.elem.attrType()}
	default:
		return schema.StringAttribute{Computed: true, MarkdownDescription: n.desc}
	}
}

func attributes(fs []field) map[string]schema.Attribute {
	m := make(map[string]schema.Attribute, len(fs))
	for _, fl := range fs {
		m[fl.name] = fl.n.attribute()
	}
	return m
}

// value converts a decoded API value (a client.Document tree with json.Number numbers) into the node's framework
// value. A missing or null value is null of the node's type; a value of an unexpected JSON type is a diagnostic.
func (n node) value(v any) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics
	bad := func() (attr.Value, diag.Diagnostics) {
		diags.AddError("Unexpected Ledger answer", fmt.Sprintf("Expected %s but the API sent %T. The provider and the Ledger server may not match.", n.kindName(), v))
		return nullOf(n.attrType()), diags
	}
	if v == nil {
		return nullOf(n.attrType()), nil
	}
	switch n.kind {
	case kStr:
		switch x := v.(type) {
		case string:
			return types.StringValue(x), nil
		case json.Number:
			return types.StringValue(x.String()), nil
		}
		return bad()
	case kBool:
		if b, ok := v.(bool); ok {
			return types.BoolValue(b), nil
		}
		return bad()
	case kInt:
		if x, ok := v.(json.Number); ok {
			if i, err := x.Int64(); err == nil {
				return types.Int64Value(i), nil
			}
		}
		return bad()
	case kJSON:
		raw, err := json.Marshal(v)
		if err != nil {
			return bad()
		}
		return types.StringValue(string(raw)), nil
	case kObj:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		vals := map[string]attr.Value{}
		for _, fl := range n.fields {
			fv, d := fl.n.value(m[fl.key])
			diags.Append(d...)
			vals[fl.name] = fv
		}
		o, d := types.ObjectValue(n.attrType().(types.ObjectType).AttrTypes, vals)
		diags.Append(d...)
		return o, diags
	case kList:
		arr, ok := v.([]any)
		if !ok {
			return bad()
		}
		vals := make([]attr.Value, 0, len(arr))
		for _, e := range arr {
			ev, d := n.elem.value(e)
			diags.Append(d...)
			vals = append(vals, ev)
		}
		l, d := types.ListValue(n.elem.attrType(), vals)
		diags.Append(d...)
		return l, diags
	}
	return bad()
}

func (n node) kindName() string {
	return [...]string{"a string", "a boolean", "an integer", "an object", "a list", "JSON"}[n.kind]
}

func nullOf(t attr.Type) attr.Value {
	switch tt := t.(type) {
	case types.ObjectType:
		return types.ObjectNull(tt.AttrTypes)
	case types.ListType:
		return types.ListNull(tt.ElemType)
	}
	switch t {
	case types.BoolType:
		return types.BoolNull()
	case types.Int64Type:
		return types.Int64Null()
	}
	return types.StringNull()
}
