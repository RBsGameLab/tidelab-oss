// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

namespace Tidelab.Tests.Fixture;

/// <summary>The fixture's message id enum, shaped as the client's EMessageId.</summary>
public enum Ids
{
    Ping = 7,
}

/// <summary>Shaped as the client's <c>NetMessage</c> attribute: the reader matches it by the
/// name's ending and its one integer argument.</summary>
[AttributeUsage(AttributeTargets.Class, Inherited = false)]
public sealed class NetMessageAttribute(int id) : Attribute
{
    public int Id { get; } = id;
}

[NetMessage(7)]
public sealed partial class Ping;
