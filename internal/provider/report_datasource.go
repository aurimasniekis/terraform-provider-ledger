package provider

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// Defaults and bounds of max_items on the list data sources.
const (
	defaultMaxItems = 1000
	maxMaxItems     = 10000
)

type inputKind int

const (
	inStr inputKind = iota
	inBool
	inStrList
)

// input is a filter or key of a report data source. param is the API query parameter (or "" for a path key).
type input struct {
	name     string
	param    string
	kind     inputKind
	required bool
	desc     string
	isID     bool
	pattern  *regexp.Regexp
	patDesc  string
}

// report describes one read-only data source: the endpoint, its inputs and what it returns.
type report struct {
	typeName string
	desc     string
	path     string // "/occurrences/{id}": {id} is replaced by the input of the same name
	inputs   []input
	paged    bool    // a cursor-paged list: the items are returned as "items", with max_items and truncated
	items    node    // the item node of a paged list
	out      []field // the fields of the answer document, for everything not paged
}

var monthRegexp = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

type reportDataSource struct {
	client *client.Client
	r      report
}

var (
	_ datasource.DataSource              = (*reportDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*reportDataSource)(nil)
)

func newReport(r report) func() datasource.DataSource {
	return func() datasource.DataSource { return &reportDataSource{r: r} }
}

func (d *reportDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.r.typeName
}

func (d *reportDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{}
	for _, in := range d.r.inputs {
		attrs[in.name] = in.attribute()
	}
	if d.r.paged {
		attrs["items"] = list("The items, in the order the API lists them.", d.r.items).attribute()
		attrs["truncated"] = schema.BoolAttribute{Computed: true, MarkdownDescription: "True when the API had more items than max_items; narrow the filters or raise max_items."}
		attrs["max_items"] = schema.Int64Attribute{Optional: true,
			MarkdownDescription: "The most items to read (1 to 10000); 1000 when unset.",
			Validators:          []validator.Int64{int64validator.Between(1, maxMaxItems)}}
	}
	for _, fl := range d.r.out {
		attrs[fl.name] = fl.n.attribute()
	}
	resp.Schema = schema.Schema{MarkdownDescription: d.r.desc, Attributes: attrs}
}

func (in input) attribute() schema.Attribute {
	opt, req := !in.required, in.required
	switch in.kind {
	case inBool:
		return schema.BoolAttribute{Optional: opt, Required: req, MarkdownDescription: in.desc}
	case inStrList:
		return schema.ListAttribute{Optional: opt, Required: req, ElementType: types.StringType, MarkdownDescription: in.desc}
	}
	var vs []validator.String
	if in.isID {
		vs = append(vs, idValidator())
	}
	if in.pattern != nil {
		vs = append(vs, stringvalidator.RegexMatches(in.pattern, in.patDesc))
	}
	return schema.StringAttribute{Optional: opt, Required: req, MarkdownDescription: in.desc, Validators: vs}
}

func (d *reportDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *reportDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	q := url.Values{}
	p := d.r.path
	for _, in := range d.r.inputs {
		switch in.kind {
		case inStr:
			var v types.String
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(in.name), &v)...)
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			if in.param == "" {
				p = strings.ReplaceAll(p, "{"+in.name+"}", url.PathEscape(v.ValueString()))
			} else {
				q.Set(in.param, v.ValueString())
			}
		case inBool:
			var v types.Bool
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(in.name), &v)...)
			if !v.IsNull() && !v.IsUnknown() {
				q.Set(in.param, strconv.FormatBool(v.ValueBool()))
			}
		case inStrList:
			var v types.List
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(in.name), &v)...)
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			var vals []string
			resp.Diagnostics.Append(v.ElementsAs(ctx, &vals, false)...)
			for _, s := range vals {
				q.Add(in.param, s)
			}
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.State.Raw = req.Config.Raw.Copy()
	set := func(name string, v attr.Value) {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(name), v)...)
	}

	if d.r.paged {
		max := int64(defaultMaxItems)
		var mi types.Int64
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("max_items"), &mi)...)
		if !mi.IsNull() && !mi.IsUnknown() {
			max = mi.ValueInt64()
		}
		items, truncated, err := d.client.ListDocuments(ctx, p, q, int(max))
		if err != nil {
			resp.Diagnostics.AddError("Reading "+d.r.typeName+" failed", err.Error())
			return
		}
		lv, diags := list("", d.r.items).value(toAnySlice(items))
		resp.Diagnostics.Append(diags...)
		set("items", lv)
		set("truncated", types.BoolValue(truncated))
		return
	}
	doc, err := d.client.GetDocument(ctx, p, q)
	if err != nil {
		resp.Diagnostics.AddError("Reading "+d.r.typeName+" failed", err.Error())
		return
	}
	for _, fl := range d.r.out {
		v, diags := fl.n.value(doc[fl.key])
		resp.Diagnostics.Append(diags...)
		set(fl.name, v)
	}
}

func toAnySlice(items []any) any { return items }
