// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-95 (3.8): Tencent PerfSight's own collection points, kept on this machine as our frame
// counters (user, 2026-11-18: "我们完全可以通过腾讯已经写好的采集来对我们自己优化").
//
// tools/cspatch (R95PerfSightTap.cs) points each call in Aki.Game to FKuroPerfSightHelper.X(...)
// at RBAuto.PerfHelper_X(...), and each call to PerfSight.X(...) at RBAuto.PerfSight_X(self, ...),
// when this file has a method of that name with the same parameters. Calls without one here still
// go to PerfSight. Aki.Core's own calls (TickSystem's over-budget values, the loading and
// GameBudget tags, network latency) are not redirected: the 3.8 build leaves Aki.Core unpatched.
//
// What the game posts through PerfSight from Aki.Game:
//   PostFrame(delta)            every frame (PerfSightController.OnTick, also while paused)
//   Begin/EndExtTag(tag)        "Battle", "Battle_<boss>_<id>" (Lord and above), "Soar", "Plot",
//                               the load and teleport phases ("Load.OpenLevel", "Teleport.LoadTeam",
//                               ...), chair summon traces, views being opened (42 call sites in 3.8)
//   PerfSight.MarkLevelLoad     "BeforeLogin", "Persistent", "Persistent_ExploreWorld_Area_<id>",
//                               "Dungeon_<instance>"; MarkLevelLoadCompleted when loading ends
//   PostValueFloat1(cat, key)   RedDot cost per frame, GC time, console command time
//   PostValueFloat3             the player's position once a second (kept for hitch lines)
//
// Each frame adds its wall time (Stopwatch between two PostFrame calls) and the engine's own
// game / render / RHI thread and present times to the current state (level, loading, paused,
// tags). Files in Win64 (kept open, flushed every 30 s, so writing costs no file opens):
//   rb_perf.tsv         every 30 s, one line per state seen in that window
//   rb_perf_hitch.tsv   one line per frame of 100 ms or more
//   rb_perf_events.tsv  level marks, PostEvent, and the posted values per window
//
// Same rules as RBAuto.cs: public game API only, no lambdas or compiler-generated types.
using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Text;
using UnrealEngine;

namespace RB;

public static partial class RBAuto
{
    private const int PerfSlots = 32;
    private const int PerfBuckets = 1024;
    private const float PerfBucketMs = 0.25f;
    private const long PerfWindowMs = 30000;
    private const float PerfHitchMs = 100f;
    private const int PerfMaxTags = 16;
    private const int PerfValueSlots = 128;

    private static readonly string[] perfKey = new string[PerfSlots];
    private static int perfUsed;
    private static readonly long[] perfFrames = new long[PerfSlots];
    private static readonly double[] perfSumMs = new double[PerfSlots];
    private static readonly double[] perfSumGt = new double[PerfSlots];
    private static readonly double[] perfSumRt = new double[PerfSlots];
    private static readonly double[] perfSumRhi = new double[PerfSlots];
    private static readonly double[] perfSumPresent = new double[PerfSlots];
    private static readonly float[] perfMaxMs = new float[PerfSlots];
    private static readonly int[] perfOverBudget = new int[PerfSlots];
    private static readonly int[] perfMaxFps = new int[PerfSlots];
    private static readonly int[] perfHistFrame = new int[PerfSlots * PerfBuckets];
    private static readonly int[] perfHistGt = new int[PerfSlots * PerfBuckets];
    private static readonly int[] perfHistRt = new int[PerfSlots * PerfBuckets];

    private static readonly string[] perfValueCat = new string[PerfValueSlots];
    private static readonly string[] perfValueName = new string[PerfValueSlots];
    private static readonly int[] perfValueCount = new int[PerfValueSlots];
    private static readonly double[] perfValueSum = new double[PerfValueSlots];
    private static readonly float[] perfValueMax = new float[PerfValueSlots];
    private static int perfValueUsed;

    private const float PerfSpanEventMs = 50f;
    private static readonly string[] perfTags = new string[PerfMaxTags];
    private static readonly long[] perfTagStart = new long[PerfMaxTags];
    private static int perfTagCount;
    private static string perfLevel = "BeforeLogin";
    private static bool perfLoaded;
    private static bool perfPaused;
    private static int perfSlot = -1;

    private static long perfLastStamp;
    private static long perfWindowStart;
    private static float perfPosX, perfPosY, perfPosZ;
    private static StreamWriter? perfOut;
    private static StreamWriter? perfHitchOut;
    private static StreamWriter? perfEventOut;
    private static bool perfOpenFailed;
    private static string perfError = "";

    // ---- FKuroPerfSightHelper (static) ----

    public static void PerfHelper_PostFrame(float deltaTime)
    {
        try
        {
            PerfFrame();
        }
        catch (Exception e)
        {
            perfError = "frame: " + e.Message;
        }
    }

    public static void PerfHelper_BeginExtTag(string tagName)
    {
        try
        {
            if (tagName == null) return;
            for (int i = 0; i < perfTagCount; i++)
                if (perfTags[i] == tagName) return;
            if (perfTagCount == PerfMaxTags) return;
            perfTagStart[perfTagCount] = Stopwatch.GetTimestamp();
            perfTags[perfTagCount++] = tagName;
            perfSlot = -1;
        }
        catch (Exception e)
        {
            perfError = "tag: " + e.Message;
        }
    }

    /// A tag's time from begin to end goes into the window's values as "tag:<name>"; a span of
    /// 50 ms or more (load and teleport phases, opening a view) also gets its own event line.
    public static void PerfHelper_EndExtTag(string tagName)
    {
        try
        {
            for (int i = 0; i < perfTagCount; i++)
            {
                if (perfTags[i] != tagName) continue;
                float ms = (float)((Stopwatch.GetTimestamp() - perfTagStart[i]) * 1000.0 / Stopwatch.Frequency);
                for (int j = i + 1; j < perfTagCount; j++)
                {
                    perfTags[j - 1] = perfTags[j];
                    perfTagStart[j - 1] = perfTagStart[j];
                }
                perfTagCount--;
                perfTags[perfTagCount] = null!;
                perfSlot = -1;
                PerfValue("tag", tagName, ms);
                if (ms >= PerfSpanEventMs) PerfEvent("span", tagName + "\t\t" + F(ms));
                return;
            }
        }
        catch (Exception e)
        {
            perfError = "tag: " + e.Message;
        }
    }

    public static void PerfHelper_PostValueFloat1(string category, string key, float valueA)
    {
        try
        {
            PerfValue(category, key, valueA);
        }
        catch (Exception e)
        {
            perfError = "value: " + e.Message;
        }
    }

    public static void PerfHelper_PostValueFloat3(string category, string key, float valueA, float valueB, float valueC)
    {
        try
        {
            if (category == "PositionAnalysis")
            {
                perfPosX = valueA;
                perfPosY = valueB;
                perfPosZ = valueC;
                return;
            }
            PerfValue(category, key, valueA);
        }
        catch (Exception e)
        {
            perfError = "value: " + e.Message;
        }
    }

    // ---- PerfSight (instance; `self` is PerfSight.Instance) ----

    public static void PerfSight_MarkLevelLoad(PerfSight self, string sceneName, int? quality)
    {
        try
        {
            perfLevel = sceneName ?? "";
            perfLoaded = false;
            perfSlot = -1;
            PerfEvent("level", perfLevel);
        }
        catch (Exception e)
        {
            perfError = "level: " + e.Message;
        }
    }

    public static void PerfSight_MarkLevelLoadCompleted(PerfSight self)
    {
        try
        {
            perfLoaded = true;
            perfSlot = -1;
            PerfEvent("loaded", perfLevel);
            QuietAceOnce();
        }
        catch (Exception e)
        {
            perfError = "level: " + e.Message;
        }
    }

    public static void PerfSight_MarkLevelFin(PerfSight self)
    {
    }

    public static void PerfSight_PostEvent(PerfSight self, int key, string? info)
    {
        try
        {
            PerfEvent("event", key.ToString(CultureInfo.InvariantCulture) + " " + (info ?? ""));
        }
        catch (Exception e)
        {
            perfError = "event: " + e.Message;
        }
    }

    public static void PerfSight_SetPcAppVersion(PerfSight self, string version)
    {
        try
        {
            PerfEvent("version", version ?? "");
        }
        catch (Exception e)
        {
            perfError = "event: " + e.Message;
        }
    }

    public static void PerfSight_SetUserId(PerfSight self, string playerId)
    {
    }

    // Lower CPU load: creates tidelab_ace.quiet after the first world load.
    private static bool aceQuietDone;

    private static void QuietAceOnce()
    {
        if (aceQuietDone) return;
        aceQuietDone = true;
        string path = Dir + "tidelab_ace.quiet";
        if (File.Exists(path)) return;
        File.WriteAllBytes(path, new byte[0]);
        PerfEvent("ace", "tidelab_ace.quiet created after the first world load");
    }

    // ---- counting ----

    private static void PerfFrame()
    {
        long stamp = Stopwatch.GetTimestamp();
        long last = perfLastStamp;
        perfLastStamp = stamp;
        long now = Environment.TickCount64;
        if (perfWindowStart == 0) perfWindowStart = now;
        if (last == 0) return;
        float ms = (float)((stamp - last) * 1000.0 / Stopwatch.Frequency);

        bool paused = TickSystem.Instance.IsPaused;
        if (paused != perfPaused)
        {
            perfPaused = paused;
            perfSlot = -1;
        }
        if (perfSlot < 0) perfSlot = PerfSlotFor(PerfStateKey());

        float gt = UKuroRenderingRuntimeBPPluginBPLibrary.GetGameThreadTime();
        float rt = UKuroRenderingRuntimeBPPluginBPLibrary.GetRenderThreadTime();
        float rhi = UKuroRenderingRuntimeBPPluginBPLibrary.GetRHIThreadTime();
        float present = UKuroRenderingRuntimeBPPluginBPLibrary.GetSwapBufferTime();
        int maxFps = UKuroRenderingRuntimeBPPluginBPLibrary.GetMaxFps();
        float budget = 1000f / (maxFps > 0 ? maxFps : 60);

        int s = perfSlot;
        perfFrames[s]++;
        perfSumMs[s] += ms;
        perfSumGt[s] += gt;
        perfSumRt[s] += rt;
        perfSumRhi[s] += rhi;
        perfSumPresent[s] += present;
        if (ms > perfMaxMs[s]) perfMaxMs[s] = ms;
        if (ms > 2 * budget) perfOverBudget[s]++;
        perfMaxFps[s] = maxFps;
        perfHistFrame[s * PerfBuckets + PerfBucket(ms)]++;
        perfHistGt[s * PerfBuckets + PerfBucket(gt)]++;
        perfHistRt[s * PerfBuckets + PerfBucket(rt)]++;

        if (ms >= PerfHitchMs) PerfHitch(ms, gt, rt, rhi, present);
        if (now - perfWindowStart >= PerfWindowMs) PerfFlush(now);
    }

    private static int PerfBucket(float ms)
    {
        if (!(ms > 0)) return 0;
        int b = (int)(ms / PerfBucketMs);
        return b < PerfBuckets ? b : PerfBuckets - 1;
    }

    private static string PerfStateKey()
    {
        StringBuilder key = new StringBuilder(perfLevel);
        if (!perfLoaded) key.Append(" loading");
        if (perfPaused) key.Append(" paused");
        // Only top-level tags (Battle, Battle_<boss>, Soar, Plot, Load, Teleport) name a state.
        // Phases ("Load.Preload") and open views ("UiViewInWindow[LoginView]") stay only as spans:
        // in the first run (2026-11-18 15:55) they split one state into dozens of rows.
        if (perfTagCount > 0)
        {
            string[] sorted = new string[perfTagCount];
            int n = 0;
            for (int i = 0; i < perfTagCount; i++)
                if (perfTags[i].IndexOf('.') < 0 && perfTags[i].IndexOf('[') < 0) sorted[n++] = perfTags[i];
            Array.Sort(sorted, 0, n, StringComparer.Ordinal);
            for (int i = 0; i < n; i++) key.Append(" +").Append(sorted[i]);
        }
        return key.ToString();
    }

    private static int PerfSlotFor(string key)
    {
        for (int i = 0; i < perfUsed; i++)
            if (perfKey[i] == key) return i;
        if (perfUsed < PerfSlots - 1)
        {
            perfKey[perfUsed] = key;
            return perfUsed++;
        }
        perfKey[PerfSlots - 1] = "(other states)";
        return PerfSlots - 1;
    }

    private static void PerfValue(string category, string key, float value)
    {
        int slot = -1;
        for (int i = 0; i < perfValueUsed; i++)
            if (perfValueName[i] == key && perfValueCat[i] == category)
            {
                slot = i;
                break;
            }
        if (slot < 0)
        {
            if (perfValueUsed == PerfValueSlots) return;
            slot = perfValueUsed++;
            perfValueCat[slot] = category ?? "";
            perfValueName[slot] = key ?? "";
        }
        perfValueCount[slot]++;
        perfValueSum[slot] += value;
        if (perfValueCount[slot] == 1 || value > perfValueMax[slot]) perfValueMax[slot] = value;
    }

    // ---- writing ----

    private static string PerfNow()
    {
        return DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff", CultureInfo.InvariantCulture);
    }

    private static string F(double v)
    {
        return v.ToString("0.00", CultureInfo.InvariantCulture);
    }

    private static StreamWriter? PerfOpen(string name, string header)
    {
        FileStream fs = new FileStream(Dir + name, FileMode.Append, FileAccess.Write, FileShare.ReadWrite | FileShare.Delete);
        StreamWriter w = new StreamWriter(fs, new UTF8Encoding(false));
        if (fs.Length == 0) w.Write(header + "\r\n");
        return w;
    }

    private static bool PerfReady()
    {
        if (perfOut != null) return true;
        if (perfOpenFailed) return false;
        try
        {
            perfOut = PerfOpen("rb_perf.tsv",
                "time\twindow_s\tstate\tframes\tseconds\tfps\tavg_ms\tp50_ms\tp95_ms\tp99_ms\tmax_ms\tgt_avg\tgt_p95\trt_avg\trt_p95\trhi_avg\tpresent_avg\tover_2x_budget\tmax_fps");
            perfHitchOut = PerfOpen("rb_perf_hitch.tsv", "time\tms\tgt\trt\trhi\tpresent\tstate\tx\ty\tz");
            perfEventOut = PerfOpen("rb_perf_events.tsv", "time\tkind\twhat\tcount\tavg\tmax");
            perfEventOut!.Write(PerfNow() + "\tstart\tRBAuto " + Version + " pid " + Environment.ProcessId + "\r\n");
            return true;
        }
        catch (Exception e)
        {
            perfOpenFailed = true;
            perfError = "open: " + e.Message;
            return false;
        }
    }

    private static void PerfEvent(string kind, string what)
    {
        if (!PerfReady()) return;
        perfEventOut!.Write(PerfNow() + "\t" + kind + "\t" + what + "\r\n");
    }

    private static void PerfHitch(float ms, float gt, float rt, float rhi, float present)
    {
        if (!PerfReady()) return;
        perfHitchOut!.Write(PerfNow() + "\t" + F(ms) + "\t" + F(gt) + "\t" + F(rt) + "\t" + F(rhi) + "\t" + F(present) + "\t" +
            perfKey[perfSlot] + "\t" + F(perfPosX) + "\t" + F(perfPosY) + "\t" + F(perfPosZ) + "\r\n");
    }

    private static double PerfPct(int[] hist, int slot, long total, double p)
    {
        long target = (long)Math.Ceiling(total * p);
        if (target < 1) target = 1;
        long seen = 0;
        int baseAt = slot * PerfBuckets;
        for (int i = 0; i < PerfBuckets; i++)
        {
            seen += hist[baseAt + i];
            if (seen >= target) return (i + 0.5) * PerfBucketMs;
        }
        return PerfBuckets * PerfBucketMs;
    }

    private static void PerfFlush(long now)
    {
        double window = (now - perfWindowStart) / 1000.0;
        perfWindowStart = now;
        if (PerfReady())
        {
            string time = PerfNow();
            for (int s = 0; s < PerfSlots; s++)
            {
                long n = perfFrames[s];
                if (n == 0) continue;
                double seconds = perfSumMs[s] / 1000.0;
                perfOut!.Write(time + "\t" + F(window) + "\t" + perfKey[s] + "\t" + n + "\t" + F(seconds) + "\t" +
                    F(seconds > 0 ? n / seconds : 0) + "\t" + F(perfSumMs[s] / n) + "\t" +
                    F(PerfPct(perfHistFrame, s, n, 0.50)) + "\t" + F(PerfPct(perfHistFrame, s, n, 0.95)) + "\t" +
                    F(PerfPct(perfHistFrame, s, n, 0.99)) + "\t" + F(perfMaxMs[s]) + "\t" +
                    F(perfSumGt[s] / n) + "\t" + F(PerfPct(perfHistGt, s, n, 0.95)) + "\t" +
                    F(perfSumRt[s] / n) + "\t" + F(PerfPct(perfHistRt, s, n, 0.95)) + "\t" +
                    F(perfSumRhi[s] / n) + "\t" + F(perfSumPresent[s] / n) + "\t" + perfOverBudget[s] + "\t" + perfMaxFps[s] + "\r\n");
            }
            for (int i = 0; i < perfValueUsed; i++)
                perfEventOut!.Write(time + "\tvalue\t" + perfValueCat[i] + ":" + perfValueName[i] + "\t" + perfValueCount[i] + "\t" +
                    F(perfValueSum[i] / perfValueCount[i]) + "\t" + F(perfValueMax[i]) + "\r\n");
            if (perfError.Length > 0)
            {
                perfEventOut!.Write(time + "\terror\t" + perfError + "\r\n");
                perfError = "";
            }
            perfOut!.Flush();
            perfHitchOut!.Flush();
            perfEventOut!.Flush();
        }
        Array.Clear(perfFrames);
        Array.Clear(perfSumMs);
        Array.Clear(perfSumGt);
        Array.Clear(perfSumRt);
        Array.Clear(perfSumRhi);
        Array.Clear(perfSumPresent);
        Array.Clear(perfMaxMs);
        Array.Clear(perfOverBudget);
        Array.Clear(perfMaxFps);
        Array.Clear(perfHistFrame);
        Array.Clear(perfHistGt);
        Array.Clear(perfHistRt);
        Array.Clear(perfKey);
        perfUsed = 0;
        perfSlot = -1;
        Array.Clear(perfValueCat);
        Array.Clear(perfValueName);
        Array.Clear(perfValueCount);
        Array.Clear(perfValueSum);
        Array.Clear(perfValueMax);
        perfValueUsed = 0;
    }
}
