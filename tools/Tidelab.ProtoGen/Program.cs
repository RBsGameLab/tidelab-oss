// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using Tidelab.ProtoGen;

// protogen <Aki.Protocol.dll> <output dir>
//   Reads the client's protocol assembly and writes, under <output dir>:
//   proto/*.proto and schema.tsv (the table the Go tests compare against); the output is
//   server/protocol, and server/tools/genproto then writes the Go package from it.
//   Exits 1 if anything in the assembly could not be read or written faithfully.
// protogen --il <dll> <type name>
//   Prints the raw IL bytes of every method of that type, for diagnosing the reader.
if (args.Length == 3 && args[0] == "--il")
{
    using var asm = new Tidelab.ProtoGen.Assembly(args[1]);
    asm.DumpIl(args[2], Console.Out);
    return 0;
}

// protogen --table <dll> <root namespace> <id enum>
//   Prints the schema table of any protoc-generated assembly (e.g. Tidelab.Tests.Fixture Ids).
if (args.Length == 4 && args[0] == "--table")
{
    var other = SchemaReader.Read(args[1], args[2], args[3], out var issues);
    Console.Out.Write(SchemaTable.Text(other));
    foreach (var p in issues)
        Console.Error.WriteLine("problem: " + p);
    return issues.Count == 0 ? 0 : 1;
}

if (args.Length != 2)
{
    Console.Error.WriteLine("usage: protogen <Aki.Protocol.dll> <output dir>   (output: server/protocol)");
    return 2;
}

var schema = SchemaReader.Read(args[0], "Aki.Protocol", "EMessageId", out var problems);
var writer = new ProtoWriter(schema);
var files = writer.Write();
problems.AddRange(writer.Problems);

string outDir = args[1];
string protoDir = Path.Combine(outDir, "proto");
Directory.CreateDirectory(protoDir);
foreach (var old in Directory.GetFiles(protoDir, "*.proto"))
    if (!files.ContainsKey(Path.GetFileName(old)))
        File.Delete(old);
foreach (var (name, text) in files)
    File.WriteAllText(Path.Combine(protoDir, name), text);
File.WriteAllText(Path.Combine(outDir, "schema.tsv"), SchemaTable.Text(schema));

int fields = schema.Messages.Sum(m => m.Fields.Count);
Console.WriteLine($"{schema.Messages.Count} messages ({schema.Messages.Count(m => m.Id is not null)} with ids), " +
    $"{fields} fields, {schema.Enums.Count} enums, {schema.MessageIds.Count} ids, {files.Count} proto files");
foreach (var p in problems)
    Console.Error.WriteLine("problem: " + p);
return problems.Count == 0 ? 0 : 1;
