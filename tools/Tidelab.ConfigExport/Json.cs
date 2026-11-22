// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Buffers;
using System.Globalization;
using System.Text;
using System.Text.Json;

namespace Tidelab.ConfigExport;

/// <summary>A JSON value as the exporter builds it: null, bool, long, double, string,
/// <see cref="JArray"/> or <see cref="JObject"/>.</summary>
public sealed class JArray : List<object?>;

/// <summary>An object keeps its keys in insertion order.</summary>
public sealed class JObject : List<KeyValuePair<string, object?>>
{
    public void Add(string key, object? value) => Add(new KeyValuePair<string, object?>(key, value));

    /// <summary>A repeated key keeps its first value, matching the exported BinData format
    /// (specs/data-tables.md §7.2; JSON.parse would keep the last).</summary>
    public void Set(string key, object? value)
    {
        if (FindIndex(kv => kv.Key == key) < 0)
            Add(key, value);
    }
}

/// <summary>JSON text the way JavaScript's JSON.stringify(value, null, 2) writes it: two-space
/// indentation, non-ASCII characters as they are, numbers as Number.prototype.toString.</summary>
public static class JsonText
{
    public static void Write(TextWriter w, object? v, int indent = 0)
    {
        switch (v)
        {
            case null: w.Write("null"); break;
            case bool b: w.Write(b ? "true" : "false"); break;
            case long l: w.Write(l.ToString(CultureInfo.InvariantCulture)); break;
            case int i: w.Write(i.ToString(CultureInfo.InvariantCulture)); break;
            case double d: w.Write(Number(d)); break;
            case string s: WriteString(w, s); break;
            case JArray a:
                if (a.Count == 0) { w.Write("[]"); break; }
                w.Write('[');
                for (int k = 0; k < a.Count; k++)
                {
                    w.Write(k == 0 ? "\n" : ",\n");
                    Indent(w, indent + 1);
                    Write(w, a[k], indent + 1);
                }
                w.Write('\n');
                Indent(w, indent);
                w.Write(']');
                break;
            case JObject o:
                if (o.Count == 0) { w.Write("{}"); break; }
                w.Write('{');
                for (int k = 0; k < o.Count; k++)
                {
                    w.Write(k == 0 ? "\n" : ",\n");
                    Indent(w, indent + 1);
                    WriteString(w, o[k].Key);
                    w.Write(": ");
                    Write(w, o[k].Value, indent + 1);
                }
                w.Write('\n');
                Indent(w, indent);
                w.Write('}');
                break;
            default: throw new ArgumentException($"not a JSON value: {v.GetType()}");
        }
    }

    public static string ToText(object? v)
    {
        var sw = new StringWriter { NewLine = "\n" };
        Write(sw, v);
        return sw.ToString();
    }

    private static void Indent(TextWriter w, int n)
    {
        for (int k = 0; k < n; k++) w.Write("  ");
    }

    public static void WriteString(TextWriter w, string s)
    {
        w.Write('"');
        for (int i = 0; i < s.Length; i++)
        {
            char c = s[i];
            switch (c)
            {
                case '"': w.Write("\\\""); break;
                case '\\': w.Write("\\\\"); break;
                case '\b': w.Write("\\b"); break;
                case '\f': w.Write("\\f"); break;
                case '\n': w.Write("\\n"); break;
                case '\r': w.Write("\\r"); break;
                case '\t': w.Write("\\t"); break;
                default:
                    if (char.IsHighSurrogate(c) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]))
                    {
                        w.Write(c);
                        w.Write(s[++i]);
                    }
                    else if (c < 0x20 || char.IsSurrogate(c))
                        w.Write($"\\u{(int)c:x4}"); // control characters and lone surrogates
                    else
                        w.Write(c);
                    break;
            }
        }
        w.Write('"');
    }

    /// <summary>Number.prototype.toString for a double (ECMAScript Number::toString, radix 10),
    /// and null for NaN and the infinities, as JSON.stringify writes them.</summary>
    public static string Number(double d)
    {
        if (double.IsNaN(d) || double.IsInfinity(d))
            return "null";
        if (d == 0)
            return "0";
        var sb = new StringBuilder();
        if (d < 0) { sb.Append('-'); d = -d; }
        // Shortest digits that round-trip, and the decimal exponent n: value = 0.digits * 10^n.
        string shortest = d.ToString("R", CultureInfo.InvariantCulture);
        (string digits, int n) = Digits(shortest);
        int k = digits.Length;
        if (k <= n && n <= 21)
            sb.Append(digits).Append('0', n - k);
        else if (0 < n && n <= 21)
            sb.Append(digits, 0, n).Append('.').Append(digits, n, k - n);
        else if (-6 < n && n <= 0)
            sb.Append("0.").Append('0', -n).Append(digits);
        else
        {
            int e = n - 1;
            sb.Append(digits[0]);
            if (k > 1) sb.Append('.').Append(digits, 1, k - 1);
            sb.Append('e').Append(e > 0 ? '+' : '-').Append(Math.Abs(e).ToString(CultureInfo.InvariantCulture));
        }
        return sb.ToString();
    }

    /// <summary>The significant digits of a positive "R" string, and n with value = 0.digits * 10^n.</summary>
    private static (string Digits, int N) Digits(string r)
    {
        int exp = 0;
        int e = r.IndexOfAny(['E', 'e']);
        if (e >= 0)
        {
            exp = int.Parse(r[(e + 1)..], CultureInfo.InvariantCulture);
            r = r[..e];
        }
        int dot = r.IndexOf('.');
        string intPart = dot < 0 ? r : r[..dot];
        string frac = dot < 0 ? "" : r[(dot + 1)..];
        string all = intPart + frac;
        int lead = 0;
        while (lead < all.Length - 1 && all[lead] == '0') lead++;
        string digits = all[lead..].TrimEnd('0');
        if (digits.Length == 0) digits = "0";
        int n = intPart.Length - lead + exp;
        return (digits, n);
    }

    /// <summary>JSON.parse, except that a repeated key keeps its first value: numbers become
    /// doubles, as in JavaScript. Null when the text is not exactly one JSON value.</summary>
    public static object? Parse(string text, out bool ok)
    {
        ok = false;
        try
        {
            var bytes = Encoding.UTF8.GetBytes(text);
            var reader = new Utf8JsonReader(bytes, new JsonReaderOptions { CommentHandling = JsonCommentHandling.Disallow });
            if (!reader.Read())
                return null;
            var v = ReadValue(ref reader);
            if (reader.Read())
                return null;
            ok = true;
            return v;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static object? ReadValue(ref Utf8JsonReader r)
    {
        switch (r.TokenType)
        {
            case JsonTokenType.Null: return null;
            case JsonTokenType.True: return true;
            case JsonTokenType.False: return false;
            case JsonTokenType.String: return r.GetString();
            case JsonTokenType.Number:
            {
                var raw = r.HasValueSequence ? r.ValueSequence.ToArray() : r.ValueSpan.ToArray();
                double d = double.Parse(Encoding.ASCII.GetString(raw), NumberStyles.Float, CultureInfo.InvariantCulture);
                return Integral(d);
            }
            case JsonTokenType.StartArray:
            {
                var a = new JArray();
                while (r.Read() && r.TokenType != JsonTokenType.EndArray)
                    a.Add(ReadValue(ref r));
                return a;
            }
            case JsonTokenType.StartObject:
            {
                var o = new JObject();
                while (r.Read() && r.TokenType != JsonTokenType.EndObject)
                {
                    string key = r.GetString()!;
                    r.Read();
                    o.Set(key, ReadValue(ref r));
                }
                return o;
            }
            default: throw new JsonException($"unexpected {r.TokenType}");
        }
    }

    /// <summary>A double that holds a whole number small enough to be exact becomes a long, so
    /// it prints and compares like the integers of the flat fields.</summary>
    public static object Integral(double d) =>
        d == Math.Floor(d) && Math.Abs(d) < 9007199254740992.0 && !(d == 0 && double.IsNegative(d)) ? (object)(long)d : d;
}
