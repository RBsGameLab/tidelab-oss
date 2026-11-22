// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Collections.Immutable;
using System.Reflection.Metadata;
using System.Reflection.Metadata.Ecma335;
using System.Reflection.PortableExecutable;

namespace Tidelab.ProtoGen;

/// <summary>A type as a signature names it: full name with '.' between namespace parts and
/// '/' between a nested type and its outer type, plus generic arguments.</summary>
public sealed record TypeSig(string Name, ImmutableArray<TypeSig> Args)
{
    public static TypeSig Of(string name) => new(name, []);

    /// <summary>The name without its namespace and outer types.</summary>
    public string ShortName
    {
        get
        {
            int cut = Math.Max(Name.LastIndexOf('/'), Name.LastIndexOf('.'));
            return Name[(cut + 1)..];
        }
    }

    public override string ToString() =>
        Args.IsEmpty ? Name : $"{Name}<{string.Join(",", Args)}>";
}

/// <summary>One IL instruction we care about: a call, a constant, or a static field store.
/// Integer constants (ldc.i4, ldc.i8) are in Int, floating ones (ldc.r4, ldc.r8) in Real.</summary>
public readonly record struct Instr(ILOpCode Op, int Offset, long Int, string? Member, string? Owner, double Real = 0);

/// <summary>An assembly read through System.Reflection.Metadata. Nothing in it is loaded or run.</summary>
public sealed class Assembly : IDisposable
{
    private readonly PEReader pe;
    public MetadataReader Md { get; }
    private readonly SigProvider sigs;

    public Assembly(string path)
    {
        pe = new PEReader(File.OpenRead(path));
        Md = pe.GetMetadataReader();
        sigs = new SigProvider(this);
    }

    public void Dispose() => pe.Dispose();

    public IEnumerable<TypeDefinitionHandle> Types => Md.TypeDefinitions;

    public string FullName(TypeDefinitionHandle h)
    {
        var t = Md.GetTypeDefinition(h);
        string name = Md.GetString(t.Name);
        var outer = t.GetDeclaringType();
        if (!outer.IsNil)
            return FullName(outer) + "/" + name;
        string ns = Md.GetString(t.Namespace);
        return ns.Length == 0 ? name : ns + "." + name;
    }

    public string FullName(TypeReferenceHandle h)
    {
        var t = Md.GetTypeReference(h);
        string name = Md.GetString(t.Name);
        if (t.ResolutionScope.Kind == HandleKind.TypeReference)
            return FullName((TypeReferenceHandle)t.ResolutionScope) + "/" + name;
        string ns = Md.GetString(t.Namespace);
        return ns.Length == 0 ? name : ns + "." + name;
    }

    public string TypeName(EntityHandle h) => h.Kind switch
    {
        HandleKind.TypeDefinition => FullName((TypeDefinitionHandle)h),
        HandleKind.TypeReference => FullName((TypeReferenceHandle)h),
        HandleKind.TypeSpecification => Md.GetTypeSpecification((TypeSpecificationHandle)h)
            .DecodeSignature(sigs, null).Name,
        _ => "",
    };

    public string? BaseType(TypeDefinitionHandle h)
    {
        var b = Md.GetTypeDefinition(h).BaseType;
        return b.IsNil ? null : TypeName(b);
    }

    public IEnumerable<string> Interfaces(TypeDefinitionHandle h) =>
        Md.GetTypeDefinition(h).GetInterfaceImplementations()
            .Select(i => TypeName(Md.GetInterfaceImplementation(i).Interface));

    public string Name(TypeDefinitionHandle h) => Md.GetString(Md.GetTypeDefinition(h).Name);

    public IEnumerable<TypeDefinitionHandle> Nested(TypeDefinitionHandle h) =>
        Md.GetTypeDefinition(h).GetNestedTypes();

    public bool IsNested(TypeDefinitionHandle h) => !Md.GetTypeDefinition(h).GetDeclaringType().IsNil;

    /// <summary>Fields in declaration order: name, whether static literal, constant value.</summary>
    public IEnumerable<(string Name, FieldDefinitionHandle Handle, object? Constant)> Fields(TypeDefinitionHandle h)
    {
        foreach (var fh in Md.GetTypeDefinition(h).GetFields())
        {
            var f = Md.GetFieldDefinition(fh);
            object? value = null;
            var ch = f.GetDefaultValue();
            if (!ch.IsNil)
                value = ReadConstant(Md.GetConstant(ch));
            yield return (Md.GetString(f.Name), fh, value);
        }
    }

    public IEnumerable<CustomAttributeHandle> FieldAttributes(FieldDefinitionHandle h) =>
        Md.GetFieldDefinition(h).GetCustomAttributes();

    public IEnumerable<CustomAttributeHandle> TypeAttributes(TypeDefinitionHandle h) =>
        Md.GetTypeDefinition(h).GetCustomAttributes();

    /// <summary>The attribute's type name and its first fixed argument, if that is a string or an integer.</summary>
    public (string Type, object? FirstArg) Attribute(CustomAttributeHandle h)
    {
        var a = Md.GetCustomAttribute(h);
        string type = a.Constructor.Kind switch
        {
            HandleKind.MemberReference => TypeName(Md.GetMemberReference((MemberReferenceHandle)a.Constructor).Parent),
            HandleKind.MethodDefinition => FullName(Md.GetMethodDefinition((MethodDefinitionHandle)a.Constructor).GetDeclaringType()),
            _ => "",
        };
        var blob = Md.GetBlobReader(a.Value);
        if (blob.Length < 2 || blob.ReadUInt16() != 1)
            return (type, null);
        // The first fixed argument: a SerString (string attributes) or an int32 (enum attributes).
        // We only use attributes whose single argument is one of those two.
        var ctorParams = ConstructorParameters(a.Constructor);
        if (ctorParams.Length == 0)
            return (type, null);
        object? first = ctorParams[0] == "System.String" ? blob.ReadSerializedString() : blob.ReadInt32();
        return (type, first);
    }

    private ImmutableArray<string> ConstructorParameters(EntityHandle ctor)
    {
        BlobHandle sig = ctor.Kind == HandleKind.MemberReference
            ? Md.GetMemberReference((MemberReferenceHandle)ctor).Signature
            : Md.GetMethodDefinition((MethodDefinitionHandle)ctor).Signature;
        var blob = Md.GetBlobReader(sig);
        var decoded = new SignatureDecoder<TypeSig, object?>(sigs, Md, null).DecodeMethodSignature(ref blob);
        // Enum parameters decode as their enum type; the blob still holds the underlying int32.
        return decoded.ParameterTypes.Select(p => p.Name).ToImmutableArray();
    }

    public IEnumerable<(string Name, TypeSig Type)> Properties(TypeDefinitionHandle h)
    {
        foreach (var ph in Md.GetTypeDefinition(h).GetProperties())
        {
            var p = Md.GetPropertyDefinition(ph);
            string name = Md.GetString(p.Name);
            if (name.Contains('.'))
                continue; // explicit interface implementation
            var sig = p.DecodeSignature(sigs, null);
            yield return (name, sig.ReturnType);
        }
    }

    public IEnumerable<(string Name, MethodDefinitionHandle Handle)> Methods(TypeDefinitionHandle h) =>
        Md.GetTypeDefinition(h).GetMethods().Select(m => (Md.GetString(Md.GetMethodDefinition(m).Name), m));

    /// <summary>Instance properties with a getter: name, type and the getter's handle.</summary>
    public IEnumerable<(string Name, TypeSig Type, MethodDefinitionHandle Getter)> Getters(TypeDefinitionHandle h)
    {
        foreach (var ph in Md.GetTypeDefinition(h).GetProperties())
        {
            var p = Md.GetPropertyDefinition(ph);
            var getter = p.GetAccessors().Getter;
            string name = Md.GetString(p.Name);
            if (getter.IsNil || name.Contains('.'))
                continue;
            if ((Md.GetMethodDefinition(getter).Attributes & System.Reflection.MethodAttributes.Static) != 0)
                continue;
            yield return (name, p.DecodeSignature(sigs, null).ReturnType, getter);
        }
    }

    /// <summary>A method's return and parameter types, and whether it is static.</summary>
    public (TypeSig Return, ImmutableArray<TypeSig> Parameters, bool Static) Signature(MethodDefinitionHandle h)
    {
        var m = Md.GetMethodDefinition(h);
        var sig = m.DecodeSignature(sigs, null);
        return (sig.ReturnType, sig.ParameterTypes, (m.Attributes & System.Reflection.MethodAttributes.Static) != 0);
    }

    public void DumpIl(string typeName, TextWriter w)
    {
        foreach (var t in Types.Where(t => Name(t) == typeName))
            foreach (var (name, m) in Methods(t))
            {
                var def = Md.GetMethodDefinition(m);
                if (def.RelativeVirtualAddress == 0)
                    continue;
                var bytes = pe.GetMethodBody(def.RelativeVirtualAddress).GetILBytes() ?? [];
                w.WriteLine($"{FullName(t)}::{name} ({bytes.Length} bytes) {Convert.ToHexString(bytes)}");
            }
    }

    /// <summary>The calls, integer constants and static field stores in a method body, in order.</summary>
    public List<Instr> Instructions(MethodDefinitionHandle h)
    {
        var result = new List<Instr>();
        var m = Md.GetMethodDefinition(h);
        if (m.RelativeVirtualAddress == 0)
            return result;
        var il = pe.GetMethodBody(m.RelativeVirtualAddress).GetILReader();
        while (il.RemainingBytes > 0)
        {
            int offset = il.Offset;
            int b = il.ReadByte();
            var op = b == 0xFE ? (ILOpCode)(0xFE00 | il.ReadByte()) : (ILOpCode)b;
            switch (op)
            {
                case >= ILOpCode.Ldc_i4_m1 and <= ILOpCode.Ldc_i4_8:
                    result.Add(new Instr(ILOpCode.Ldc_i4, offset, (int)op - (int)ILOpCode.Ldc_i4_0, null, null)); // ldc.i4.m1 is -1
                    continue;
                case ILOpCode.Ldc_i4_s:
                    result.Add(new Instr(ILOpCode.Ldc_i4, offset, il.ReadSByte(), null, null));
                    continue;
                case ILOpCode.Ldc_i4:
                    result.Add(new Instr(ILOpCode.Ldc_i4, offset, il.ReadInt32(), null, null));
                    continue;
                case ILOpCode.Ldc_i8:
                    result.Add(new Instr(op, offset, il.ReadInt64(), null, null));
                    continue;
                case ILOpCode.Ldc_r4:
                    result.Add(new Instr(op, offset, 0, null, null, il.ReadSingle()));
                    continue;
                case ILOpCode.Ldc_r8:
                    result.Add(new Instr(op, offset, 0, null, null, il.ReadDouble()));
                    continue;
                case ILOpCode.Call or ILOpCode.Callvirt or ILOpCode.Newobj:
                {
                    int token = il.ReadInt32();
                    (string owner, string member) = ("", "");
                    try { (owner, member) = Member(MetadataTokens.EntityHandle(token)); }
                    catch (BadImageFormatException e) { throw new BadImageFormatException($"{FullName(m.GetDeclaringType())}::{Md.GetString(m.Name)} IL_{offset:X4}: {op} with token {token:X8}", e); }
                    result.Add(new Instr(op, offset, 0, member, owner));
                    continue;
                }
                case ILOpCode.Stsfld or ILOpCode.Ldsfld:
                {
                    var (owner, member) = Member(MetadataTokens.EntityHandle(il.ReadInt32()));
                    result.Add(new Instr(op, offset, 0, member, owner));
                    continue;
                }
                case ILOpCode.Switch:
                {
                    int targets = il.ReadInt32(); // apart: `Offset += 4 * ReadInt32()` reads Offset first
                    il.Offset += 4 * targets;
                    result.Add(new Instr(op, offset, 0, null, null));
                    continue;
                }
            }
            il.Offset += OperandSize(op);
            result.Add(new Instr(op, offset, 0, null, null));
        }
        return result;
    }

    /// <summary>A method or field token as (declaring type, member name).</summary>
    private (string Owner, string Name) Member(EntityHandle h)
    {
        switch (h.Kind)
        {
            case HandleKind.MethodDefinition:
            {
                var m = Md.GetMethodDefinition((MethodDefinitionHandle)h);
                return (FullName(m.GetDeclaringType()), Md.GetString(m.Name));
            }
            case HandleKind.FieldDefinition:
            {
                var f = Md.GetFieldDefinition((FieldDefinitionHandle)h);
                return (FullName(f.GetDeclaringType()), Md.GetString(f.Name));
            }
            case HandleKind.MemberReference:
            {
                var r = Md.GetMemberReference((MemberReferenceHandle)h);
                string owner = r.Parent.Kind switch
                {
                    HandleKind.MethodDefinition => FullName(Md.GetMethodDefinition((MethodDefinitionHandle)r.Parent).GetDeclaringType()),
                    _ => TypeName(r.Parent),
                };
                return (owner, Md.GetString(r.Name));
            }
            case HandleKind.MethodSpecification:
                return Member(Md.GetMethodSpecification((MethodSpecificationHandle)h).Method);
            default:
                return ("", "");
        }
    }

    private static int OperandSize(ILOpCode op) => op switch
    {
        ILOpCode.Ldarg_s or ILOpCode.Ldarga_s or ILOpCode.Starg_s or ILOpCode.Ldloc_s
            or ILOpCode.Ldloca_s or ILOpCode.Stloc_s or ILOpCode.Unaligned => 1,
        (ILOpCode)0xFE19 => 1, // no.
        >= ILOpCode.Br_s and <= ILOpCode.Blt_un_s => 1,
        ILOpCode.Leave_s => 1,
        ILOpCode.Ldarg or ILOpCode.Ldarga or ILOpCode.Starg or ILOpCode.Ldloc
            or ILOpCode.Ldloca or ILOpCode.Stloc => 2,
        ILOpCode.Ldc_i8 or ILOpCode.Ldc_r8 => 8,
        >= ILOpCode.Br and <= ILOpCode.Blt_un => 4,
        ILOpCode.Ldc_r4 or ILOpCode.Jmp or ILOpCode.Calli or ILOpCode.Cpobj or ILOpCode.Ldobj
            or ILOpCode.Ldstr or ILOpCode.Castclass or ILOpCode.Isinst or ILOpCode.Unbox
            or ILOpCode.Ldfld or ILOpCode.Ldflda or ILOpCode.Stfld or ILOpCode.Ldsflda
            or ILOpCode.Stobj or ILOpCode.Box or ILOpCode.Newarr or ILOpCode.Ldelema
            or ILOpCode.Ldelem or ILOpCode.Stelem or ILOpCode.Unbox_any or ILOpCode.Refanyval
            or ILOpCode.Mkrefany or ILOpCode.Ldtoken or ILOpCode.Leave or ILOpCode.Ldftn
            or ILOpCode.Ldvirtftn or ILOpCode.Initobj or ILOpCode.Constrained or ILOpCode.Sizeof => 4,
        _ => 0,
    };

    private object? ReadConstant(Constant c)
    {
        var r = Md.GetBlobReader(c.Value);
        return c.TypeCode switch
        {
            ConstantTypeCode.Int32 => r.ReadInt32(),
            ConstantTypeCode.UInt32 => (int)r.ReadUInt32(),
            ConstantTypeCode.Int16 => (int)r.ReadInt16(),
            ConstantTypeCode.UInt16 => (int)r.ReadUInt16(),
            ConstantTypeCode.Byte => (int)r.ReadByte(),
            ConstantTypeCode.SByte => (int)r.ReadSByte(),
            ConstantTypeCode.Int64 => r.ReadInt64(),
            ConstantTypeCode.String => r.ReadUTF16(r.Length),
            _ => null,
        };
    }

    private sealed class SigProvider(Assembly asm) : ISignatureTypeProvider<TypeSig, object?>
    {
        public TypeSig GetPrimitiveType(PrimitiveTypeCode code) => TypeSig.Of("System." + code switch
        {
            PrimitiveTypeCode.Boolean => "Boolean",
            PrimitiveTypeCode.Int32 => "Int32",
            PrimitiveTypeCode.UInt32 => "UInt32",
            PrimitiveTypeCode.Int64 => "Int64",
            PrimitiveTypeCode.UInt64 => "UInt64",
            PrimitiveTypeCode.Single => "Single",
            PrimitiveTypeCode.Double => "Double",
            PrimitiveTypeCode.String => "String",
            PrimitiveTypeCode.Object => "Object",
            PrimitiveTypeCode.Void => "Void",
            _ => code.ToString(),
        });

        public TypeSig GetTypeFromDefinition(MetadataReader reader, TypeDefinitionHandle handle, byte rawTypeKind) =>
            TypeSig.Of(asm.FullName(handle));

        public TypeSig GetTypeFromReference(MetadataReader reader, TypeReferenceHandle handle, byte rawTypeKind) =>
            TypeSig.Of(asm.FullName(handle));

        public TypeSig GetGenericInstantiation(TypeSig genericType, ImmutableArray<TypeSig> typeArguments) =>
            new(genericType.Name, typeArguments);

        public TypeSig GetSZArrayType(TypeSig elementType) => TypeSig.Of(elementType.Name + "[]");
        public TypeSig GetArrayType(TypeSig elementType, ArrayShape shape) => TypeSig.Of(elementType.Name + "[,]");
        public TypeSig GetByReferenceType(TypeSig elementType) => elementType;
        public TypeSig GetPointerType(TypeSig elementType) => TypeSig.Of(elementType.Name + "*");
        public TypeSig GetPinnedType(TypeSig elementType) => elementType;
        public TypeSig GetGenericTypeParameter(object? context, int index) => TypeSig.Of("!" + index);
        public TypeSig GetGenericMethodParameter(object? context, int index) => TypeSig.Of("!!" + index);
        public TypeSig GetFunctionPointerType(MethodSignature<TypeSig> signature) => TypeSig.Of("fnptr");
        public TypeSig GetModifiedType(TypeSig modifier, TypeSig unmodifiedType, bool isRequired) => unmodifiedType;
        public TypeSig GetTypeFromSpecification(MetadataReader reader, object? context, TypeSpecificationHandle handle, byte rawTypeKind) =>
            reader.GetTypeSpecification(handle).DecodeSignature(this, context);
    }
}
