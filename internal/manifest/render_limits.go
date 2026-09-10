package manifest

import (
	"errors"
	"reflect"
	"text/template/parse"
)

// Bound work even when a template emits nothing (output limits alone cannot do that).
func validateRenderTree(root *parse.ListNode) error {
	nodes := 0
	var walk func(parse.Node, int) error
	walk = func(n parse.Node, ranges int) error {
		if n == nil || reflect.ValueOf(n).IsNil() {
			return nil
		}
		nodes++
		if nodes > 2000 {
			return errors.New("template exceeds 2000 syntax nodes")
		}
		switch node := n.(type) {
		case *parse.ListNode:
			for _, child := range node.Nodes {
				if err := walk(child, ranges); err != nil {
					return err
				}
			}
		case *parse.RangeNode:
			if ranges > 0 {
				return errors.New("nested template ranges are not supported")
			}
			if err := walk(node.List, ranges+1); err != nil {
				return err
			}
			return walk(node.ElseList, ranges+1)
		case *parse.IfNode:
			if err := walk(node.List, ranges); err != nil {
				return err
			}
			return walk(node.ElseList, ranges)
		case *parse.WithNode:
			if err := walk(node.List, ranges); err != nil {
				return err
			}
			return walk(node.ElseList, ranges)
		case *parse.TemplateNode:
			return errors.New("template invocation is not supported; use separate bounded source files")
		}
		return nil
	}
	return walk(root, 0)
}

func validateVariables(vars map[string]any) error {
	count := 0
	bytes := 0
	var walk func(reflect.Value, int) error
	walk = func(v reflect.Value, depth int) error {
		count++
		if count > 1000 || depth > 16 {
			return errors.New("render variables exceed 1000 values or 16 nesting levels")
		}
		if !v.IsValid() {
			return nil
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				return walk(v.Elem(), depth)
			}
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return errors.New("template object keys must be strings")
			}
			it := v.MapRange()
			for it.Next() {
				bytes += len(it.Key().String())
				if err := walk(it.Value(), depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if err := walk(v.Index(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.String:
			bytes += v.Len()
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return errors.New("template variables must contain only JSON-like values")
		}
		if bytes > MaxRenderSize {
			return errors.New("render variables exceed 1 MiB")
		}
		return nil
	}
	return walk(reflect.ValueOf(vars), 0)
}
