// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

namespace Tidelab.ProtoGen;

/// <summary>A protobuf scalar or reference kind, named as in a .proto file.</summary>
public enum Kind
{
    Double, Float, Int32, Int64, UInt32, UInt64, SInt32, SInt64,
    Fixed32, Fixed64, SFixed32, SFixed64, Bool, String, Bytes, Enum, Message,
}

public enum Label { Singular, Optional, Repeated, Map }

/// <summary>A field's value type. <see cref="Ref"/> is the full name of the enum or message
/// (namespace, then outer messages, joined with '.'), for those two kinds only.</summary>
public sealed record ValueType(Kind Kind, string? Ref = null)
{
    public override string ToString() => Ref ?? Kind.ToString().ToLowerInvariant();

    /// <summary>Kinds a repeated field packs by default in proto3.</summary>
    public bool Packable => Kind is not (Kind.String or Kind.Bytes or Kind.Message);
}

public sealed class Field
{
    /// <summary>The C# property name, as the client has it.</summary>
    public required string Name { get; init; }
    public required int Number { get; init; }
    public required Label Label { get; init; }
    public required ValueType Type { get; set; }
    /// <summary>Map key type, for <see cref="Label.Map"/> only.</summary>
    public ValueType? Key { get; set; }
    /// <summary>For repeated packable fields: false when the client writes them unpacked.</summary>
    public bool Packed { get; set; } = true;
    /// <summary>The oneof this field belongs to (its C# name, without "Case"), or null.</summary>
    public string? Oneof { get; init; }
}

public sealed class Message
{
    /// <summary>Namespace, outer messages and name, joined with '.'.</summary>
    public required string FullName { get; init; }
    public required string Namespace { get; init; }
    public required string Name { get; init; }
    /// <summary>Full name of the outer message, for nested messages.</summary>
    public string? Outer { get; init; }
    /// <summary>The client's message id (<c>NetMessage</c> attribute), for messages sent on their own.</summary>
    public int? Id { get; set; }
    public List<Field> Fields { get; } = [];
    public List<string> Oneofs { get; } = [];
}

public sealed class EnumType
{
    public required string FullName { get; init; }
    public required string Namespace { get; init; }
    public required string Name { get; init; }
    public string? Outer { get; init; }
    /// <summary>Values in declaration order: the protocol name (OriginalName) and number.</summary>
    public List<(string Name, int Value)> Values { get; } = [];
}

public sealed class Schema
{
    public List<Message> Messages { get; } = [];
    public List<EnumType> Enums { get; } = [];
    /// <summary>Every member of the client's <c>EMessageId</c>, name to id.</summary>
    public SortedDictionary<string, int> MessageIds { get; } = new(StringComparer.Ordinal);

    public IEnumerable<string> Namespaces =>
        Messages.Select(m => m.Namespace).Concat(Enums.Select(e => e.Namespace)).Distinct().Order(StringComparer.Ordinal);
}
