// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-55: perfect dodge in the client's C# build, like dodge mode of R-30's autocombat.js.
//
// tools/cspatch copies this type into Aki.Game.dll and makes BulletCollisionSystem.InDodge call
// TryAutoDodge when the game's own test says "no perfect dodge". When an enemy bullet that may be
// dodged (Logic.CanDodge) hits the role you control, a dodge is pressed and the hit counts as a
// perfect dodge (true), as autocombat.js did with BulletCollisionSystem.xWo.
//
// Mode: the first word of Game/Client/Binaries/Win64/auto_mode.txt, read once a second (the
// panel tools/autocombat/panel.ps1 writes it). "off"; "dodge" perfect dodge only; "counter"
// dodge plus the dodge counter and the parry window (R-75); "base" adds the rotation
// ok-ww plays for this team (the baseline to beat); "rb" plays R-75's own plan.
// An unknown word, or no file, means dodge; "team" is the old name of "base".
// Status: auto_status.txt beside it, rewritten once a second.
//
// Rules for this file: only public game API (it runs inside Aki.Game, but private members of
// other types are not accessible), no lambdas or other compiler-generated types (only the RBAuto
// type itself is copied).
using System;
using System.IO;
using AkiClient.Game.Aki.Character.BaseCharacter;
using AkiClient.Game.Aki.Character.Input.Enum;
using CSharpScript.Game.Input;

namespace RB;

public static partial class RBAuto
{
    // R-171: the client's own Binaries\Win64 (the game exe's folder), wherever the game is
    // installed; the switch files, rb_*.txt and auto_*.txt live there.
    private static readonly string Dir = GameDir();

    private static string GameDir()
    {
        try
        {
            string? exe = Environment.ProcessPath;
            string? dir = exe == null ? null : Path.GetDirectoryName(exe);
            if (!string.IsNullOrEmpty(dir))
                return dir + Path.DirectorySeparatorChar;
        }
        catch (Exception)
        {
        }
        return AppContext.BaseDirectory;
    }
    private const int Version = 4;

    // Modes, in order: each one does everything the one before it does.
    private const int ModeOff = 0;
    private const int ModeDodge = 1;
    private const int ModeCounter = 2;
    private const int ModeBase = 3;
    private const int ModeRb = 4;

    private static int mode = ModeDodge;
    private static long nextModeCheck;
    private static long lastDodge;
    private static int dodges;
    private static int blockTag;
    private static string lastError = "";

    public static bool TryAutoDodge(BulletInfo? bullet, Entity? victim)
    {
        try
        {
            long now = Environment.TickCount64;
            if (now >= nextModeCheck)
            {
                nextModeCheck = now + 1000;
                ReadMode();
                WriteStatus();
            }
            if (mode == ModeOff || bullet == null || victim == null) return false;
            if (bullet.BulletDataMain?.Logic?.CanDodge != true) return false;
            SceneTeamModel? team = SceneTeamModel.Instance;
            EntityHandle? handle = team?.GetCurrentEntity;
            WorldEntity? me = handle?.Entity;
            if (me == null || !ReferenceEquals(victim, me) || ReferenceEquals(bullet.Attacker, me)) return false;
            if (blockTag == 0) blockTag = GameplayTagDefine.EGameplayTagId["角色.Common.技能通用标识.极限闪避"];
            if (BulletUtil.DoesEntityContainsTag(victim, blockTag)) return false;
            if (now - lastDodge > 250)
            {
                lastDodge = now;
                InputController input = InputController.Instance;
                // InputController takes the struct CSharpScript.Game.Input.EInputAction, not the enum of the same name.
                input.InputAction(CSharpScript.Game.Input.EInputAction.闪避, EInputState.Press);
                input.InputAction(CSharpScript.Game.Input.EInputAction.闪避, EInputState.Release);
                dodges++;
                // R-75: the counter is a 普攻-genre skill that only exists while the role carries
                // the 极限闪避 tag, which buff 3152 grants for 0.6 s. Wait for the tag instead of
                // guessing a delay (autocombat.js pressed at a fixed 220 ms).
                if (mode >= ModeCounter) counterUntil = now + 600;
            }
            return true;
        }
        catch (Exception e)
        {
            lastError = e.GetType().Name + ": " + e.Message;
            return false;
        }
    }

    // ---- R-75: dodge counter, parry window and the rotations --------------------------------
    //
    // What the data says (R-75):
    //   * A perfect dodge sends the role the gameplay event 触发极限闪避, GA_LimitDodge runs and
    //     buff 3152 gives it 极限闪避 + invincibility for 0.6 s. While that tag is on, 普攻 plays
    //     the role's 极限闪避反击 skill (忌炎 1404407, 莫特斐 1204006, 守岸人 1505012) instead of
    //     the first normal attack, and the dodge montage does not end the skill.
    //   * An enemy's 弹反 window is TsAnimNotifyStateCounterAttack on its own animation; pressing
    //     攻击 inside it parries.
    // Keys (AkiClient...Input.Enum.EInputAction): 攻击 4, 闪避 5, 技能1 6, 幻象1 7, 大招 8,
    // 幻象2 9 (the echo skill), 切换角色1..3 10..12.
    private const int Jiyan = 1404;
    private const int Mortefi = 1204;
    private const int Shorekeeper = 1505;

    private static long counterUntil;        // press 攻击 while the 极限闪避 tag is up, until this
    private static long nextAction;          // the rotation presses nothing before this
    private static long ultWindowUntil;      // 忌炎 is inside his ult form (12 s of 普攻 + 技能)
    private static int turnRole;             // whose turn the rotation is playing
    private static int turnStep;
    private static int combatTag;
    private static int noControlTag;
    private static int counters;
    private static int parries;
    private static int switches;
    private static string planNote = "";

    /// Called at the start of TsAnimNotifyStateCounterAttack.K2_NotifyBegin_Implementation
    /// (cspatch): an enemy just opened its parry window.
    public static void OnCounterWindow(UnrealEngine.USkeletalMeshComponent? meshComp)
    {
        try
        {
            if (mode < ModeCounter || meshComp == null) return;
            UnrealEngine.AActor? owner = meshComp.GetOwner();
            TsBaseCharacter? character = owner as TsBaseCharacter;
            if (character == null) return;
            CharacterActorComponent? actorComp = character.CharacterActorComponent;
            Entity? enemy = actorComp?.Entity;
            WorldEntity? me = Me();
            if (enemy == null || me == null || ReferenceEquals(enemy, me)) return;
            Tap(CSharpScript.Game.Input.EInputAction.攻击);
            parries++;
            nextAction = Environment.TickCount64 + 350;
        }
        catch (Exception e)
        {
            lastError = "parry: " + e.Message;
        }
    }

    /// Called at the start of TsAnimNotifyStateVisionCounterAttack.K2_NotifyBegin_Implementation
    /// (cspatch): the enemy's 幻象弹反 window takes the echo skill.
    public static void OnVisionCounterWindow(UnrealEngine.USkeletalMeshComponent? meshComp)
    {
        try
        {
            if (mode < ModeCounter || meshComp == null) return;
            TsBaseCharacter? character = meshComp.GetOwner() as TsBaseCharacter;
            if (character == null) return;
            Entity? enemy = character.CharacterActorComponent?.Entity;
            WorldEntity? me = Me();
            if (enemy == null || me == null || ReferenceEquals(enemy, me)) return;
            Tap(CSharpScript.Game.Input.EInputAction.幻象2);
            parries++;
            nextAction = Environment.TickCount64 + 350;
        }
        catch (Exception e)
        {
            lastError = "vision parry: " + e.Message;
        }
    }

    private static WorldEntity? Me()
    {
        SceneTeamModel? team = SceneTeamModel.Instance;
        EntityHandle? handle = team?.GetCurrentEntity;
        return handle?.Entity;
    }

    // ---- the states the rotation reads (R-75, from the Buff table) --------------------------
    // Every one of these is a tag some buff grants, so the rotation reads what is really on the
    // characters instead of counting its own seconds. The team's other two roles keep their
    // entities while off the field, so their tags can be read as well.
    private const string TagJiyanUlt = "角色.R2T1JiyanMd10011.技能标识.大招状态";          // buff 1404300001, 10 s
    private const string TagMortefiUlt = "角色.R2T1BaerMd10011.技能ID.巴尔大招";           // buff 1204070000
    private const string TagMortefiRage = "角色.R2T1BaerMd10011.技能ID.满特殊能量";        // 怒气值 full: E becomes 怒火赋格
    private const string TagField1 = "角色.R2T1ShouanrenMd10011.动作标识.领域1";           // 浅析星域, 40 s
    private const string TagField2 = "角色.R2T1ShouanrenMd10011.动作标识.领域2";           // 深潜星域 (crit)
    private const string TagField3 = "角色.R2T1ShouanrenMd10011.动作标识.领域3";           // 解限星域 (crit damage)
    private const string TagOutroShore = "角色.R2T1ShouanrenMd10011.buff标识.退场技.buff1"; // 双联合相, 30 s, 4-5 saves

    private static int TagId(string name)
    {
        try
        {
            return GameplayTagDefine.EGameplayTagId[name];
        }
        catch (Exception)
        {
            return 0;
        }
    }

    /// The team entity of this role, on the field or not.
    private static Entity? RoleEntity(int roleId)
    {
        SceneTeamModel? team = SceneTeamModel.Instance;
        if (team == null) return null;
        System.Collections.Generic.List<EntityHandle> handles = team.GetTeamEntities(true);
        for (int i = 0; i < handles.Count; i++)
        {
            EntityHandle handle = handles[i];
            WorldEntity? entity = handle == null ? null : handle.Entity;
            if (entity != null && entity.Valid && RoleOf(entity) == roleId) return entity;
        }
        return null;
    }

    /// The skill the echo key (幻象2) would play for this role, the way
    /// InputFunctionVisionSkill2 finds it: the equipped echo's vision id decides it, so this
    /// works for summon, transform and the special echoes alike. 0 = nothing equipped.
    private static bool EchoReady(Entity entity)
    {
        CharacterVisionComponent? vision = entity.GetComponent<CharacterVisionComponent>();
        if (vision == null || !vision.Valid) return false;
        int visionId = vision.GetVisionId(null);
        if (visionId == 0) return false;
        int skillId = PhantomUtil.GetEntityVisionSkillId(entity.Id, visionId);
        return skillId != 0 && SkillReady(entity, skillId);
    }

    private static bool RoleHasTag(int roleId, string tagName)
    {
        Entity? entity = RoleEntity(roleId);
        if (entity == null) return false;
        int tag = TagId(tagName);
        return tag != 0 && BulletUtil.DoesEntityContainsTag(entity, tag);
    }

    private static void Tap(CSharpScript.Game.Input.EInputAction action)
    {
        InputController input = InputController.Instance;
        input.InputAction(action, EInputState.Press);
        input.InputAction(action, EInputState.Release);
    }

    private static void Hold(CSharpScript.Game.Input.EInputAction action)
    {
        InputController.Instance.InputAction(action, EInputState.Press);
    }

    private static void Release(CSharpScript.Game.Input.EInputAction action)
    {
        InputController.Instance.InputAction(action, EInputState.Release);
    }

    private static bool HasTag(Entity entity, string name)
    {
        return BulletUtil.DoesEntityContainsTag(entity, GameplayTagDefine.EGameplayTagId[name]);
    }

    private static float Attr(Entity entity, Aki.Protocol.EAttributeType type)
    {
        BaseAttributeComponent? attr = entity.GetComponent<BaseAttributeComponent>();
        return attr == null ? 0f : attr.GetCurrentValue(type);
    }

    private static bool Full(Entity entity, Aki.Protocol.EAttributeType value, Aki.Protocol.EAttributeType max)
    {
        float cap = Attr(entity, max);
        return cap > 0f && Attr(entity, value) >= cap - 0.01f;
    }

    private static bool SkillReady(Entity entity, int skillId)
    {
        CharacterSkillComponent? skills = entity.GetComponent<CharacterSkillComponent>();
        return skills != null && skills.Valid && skills.GetSkillInfo(skillId) != null && skills.IsCanUseSkill(skillId);
    }

    /// The first of these skills the role can use now, or 0.
    private static int FirstReady(Entity entity, int a, int b)
    {
        if (SkillReady(entity, a)) return a;
        if (b != 0 && SkillReady(entity, b)) return b;
        return 0;
    }

    private static int RoleOf(Entity entity)
    {
        CreatureDataComponent? data = entity.GetComponent<CreatureDataComponent>();
        return data == null || !data.Valid ? 0 : data.GetRoleId();
    }

    /// Presses the team key of this role, if it is in the team and not already out front.
    private static bool SwitchTo(int roleId)
    {
        SceneTeamModel? team = SceneTeamModel.Instance;
        if (team == null) return false;
        System.Collections.Generic.List<int> ids = team.GetTeamRoleConfigIdList(true, false);
        for (int i = 0; i < ids.Count && i < 3; i++)
        {
            if (ids[i] != roleId) continue;
            if (i == 0) Tap(CSharpScript.Game.Input.EInputAction.切换角色1);
            else if (i == 1) Tap(CSharpScript.Game.Input.EInputAction.切换角色2);
            else Tap(CSharpScript.Game.Input.EInputAction.切换角色3);
            switches++;
            return true;
        }
        return false;
    }

    /// The next role of the team after this one (the order the keys are in).
    private static int NextRole(int roleId)
    {
        SceneTeamModel? team = SceneTeamModel.Instance;
        if (team == null) return 0;
        System.Collections.Generic.List<int> ids = team.GetTeamRoleConfigIdList(true, false);
        for (int i = 0; i < ids.Count; i++)
            if (ids[i] == roleId) return ids[(i + 1) % ids.Count];
        return 0;
    }

    /// Runs every tick from OnGameTick. Each press sets nextAction, so one key per window.
    private static void RunRotation(long now)
    {
        if (mode < ModeCounter) return;
        WorldEntity? me = Me();
        if (me == null || !me.Valid) return;

        // The dodge counter: as soon as the perfect-dodge tag is really on, press 普攻.
        if (counterUntil > 0)
        {
            if (blockTag == 0) blockTag = GameplayTagDefine.EGameplayTagId["角色.Common.技能通用标识.极限闪避"];
            if (now > counterUntil)
            {
                counterUntil = 0;
            }
            else if (BulletUtil.DoesEntityContainsTag(me, blockTag))
            {
                counterUntil = 0;
                Tap(CSharpScript.Game.Input.EInputAction.攻击);
                counters++;
                nextAction = now + 450;   // the counter montage opens its next-attack window at 0.67 s
                return;
            }
        }
        if (mode < ModeBase || now < nextAction) return;
        // The same guard the game's own input functions use (staggered, in water, climbing,
        // grabbed, transforming), plus "the front role is not under control" (a cutscene).
        if (!InputFunctionCommon.CanResponseInput(me)) return;
        if (noControlTag == 0) noControlTag = GameplayTagDefine.EGameplayTagId["行为状态.逻辑状态.前台不受控制"];
        if (BulletUtil.DoesEntityContainsTag(me, noControlTag)) return;
        if (combatTag == 0) combatTag = GameplayTagDefine.EGameplayTagId["行为状态.逻辑状态.进入战斗"];
        if (!BulletUtil.DoesEntityContainsTag(me, combatTag))
        {
            turnRole = 0;
            ultWindowUntil = 0;
            return;
        }
        int role = RoleOf(me);
        if (role != turnRole)
        {
            turnRole = role;
            turnStep = 0;
        }
        if (mode == ModeRb) PlayRb(me, role, now);
        else PlayBase(me, role, now);
    }

    /// ok-ww's order for this team (src/char/Jiyan.py, Mortefi.py, ShoreKeeper.py),
    /// the baseline the R-75 plan has to beat.
    private static void PlayBase(WorldEntity me, int role, long now)
    {
        planNote = "base " + role;
        if (role == Jiyan)
        {
            int ult = FirstReady(me, 1404304, 1404300);
            if (ult != 0 && Full(me, Aki.Protocol.EAttributeType.Energy, Aki.Protocol.EAttributeType.EnergyMax))
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);
                ultWindowUntil = now + 12000;
                nextAction = now + 1500;
                return;
            }
            if (now < ultWindowUntil)
            {
                // 12 s of normal attacks with the skill whenever it comes back.
                if (FirstReady(me, 1404201, 1404200) != 0) Tap(CSharpScript.Game.Input.EInputAction.技能1);
                else Tap(CSharpScript.Game.Input.EInputAction.攻击);
                nextAction = now + 400;
                return;
            }
            if (Full(me, Aki.Protocol.EAttributeType.SpecialEnergy1, Aki.Protocol.EAttributeType.SpecialEnergy1Max) ||
                Full(me, Aki.Protocol.EAttributeType.SpecialEnergy4, Aki.Protocol.EAttributeType.SpecialEnergy4Max))
            {
                if (FirstReady(me, 1404201, 1404200) != 0)
                {
                    Tap(CSharpScript.Game.Input.EInputAction.技能1);
                    nextAction = now + 900;
                    return;
                }
                if (EchoReady(me))
                {
                    Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                    nextAction = now + 900;
                    return;
                }
                SwitchTo(NextRole(role));
                nextAction = now + 900;
                return;
            }
            // Every fourth press a heavy attack (hold 攻击), as ok-ww does to fill the forte gauge.
            turnStep++;
            if (turnStep % 4 == 0)
            {
                Hold(CSharpScript.Game.Input.EInputAction.攻击);
                heavyUntil = now + 500;
                nextAction = now + 700;
                return;
            }
            Tap(CSharpScript.Game.Input.EInputAction.攻击);
            nextAction = now + 350;
            return;
        }
        if (role == Mortefi)
        {
            if (FirstReady(me, 1204070, 0) != 0 && Full(me, Aki.Protocol.EAttributeType.Energy, Aki.Protocol.EAttributeType.EnergyMax))
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);
                nextAction = now + 1800;
                return;
            }
            if (FirstReady(me, 1204021, 1204011) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.技能1);
                nextAction = now + 900;
                return;
            }
            if (EchoReady(me))
            {
                Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                nextAction = now + 900;
                return;
            }
            SwitchTo(NextRole(role));
            nextAction = now + 900;
            return;
        }
        if (role == Shorekeeper)
        {
            if (EchoReady(me))
            {
                Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                nextAction = now + 1200;
                return;
            }
            if (FirstReady(me, 1505402, 1505401) != 0 && Full(me, Aki.Protocol.EAttributeType.Energy, Aki.Protocol.EAttributeType.EnergyMax))
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);
                nextAction = now + 2500;
                return;
            }
            if (FirstReady(me, 1505301, 1505302) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.技能1);
                nextAction = now + 900;
                return;
            }
            if (Full(me, Aki.Protocol.EAttributeType.SpecialEnergy1, Aki.Protocol.EAttributeType.SpecialEnergy1Max))
            {
                Hold(CSharpScript.Game.Input.EInputAction.攻击);   // 强化重击
                heavyUntil = now + 700;
                nextAction = now + 1400;
                return;
            }
            SwitchTo(NextRole(role));
            nextAction = now + 900;
            return;
        }
        // A role without a plan: keep the player's own hands free, only attack.
        Tap(CSharpScript.Game.Input.EInputAction.攻击);
        nextAction = now + 400;
    }

    /// R-75's own plan, read off the skill descriptions (lang_multi_text) and the Buff table:
    ///   * 守岸人 ult opens 浅析星域 (buff 1505401001, 40 s). Each intro skill a team mate plays
    ///     inside it upgrades the field: 深潜星域 (crit) and then 解限星域 (crit damage).
    ///   * Her outro 双联合相 gives the role coming in 4-5 free dodges and team damage for 30 s
    ///     (buff 1505000003 / 2002202311).
    ///   * 莫特斐's ult 浮翼狂想 makes the on-field role's 普攻 and 重击 hits call his coordinated
    ///     attacks (tag 巴尔大招), and his outro 怒意移调 deepens the next role's *heavy* damage
    ///     and ends early if that role is switched away again.
    ///   * 忌炎's ult 苍躣八荒·谋定 puts him in 破阵状态 for 10 s (buff 1404300001), where 普攻,
    ///     重击 and the dodge counter all become 重击破阵之枪 — heavy hits, so they eat 怒意移调
    ///     and keep calling 莫特斐's coordinated attacks.
    /// So the order is 守岸人 -> 莫特斐 -> 忌炎, every hand-over with a full concerto (or the outro
    /// never happens), and 忌炎 keeps the field while anything is up. Against the baseline the
    /// differences are: the two field upgrades are actually collected, 莫特斐's heavy buff is
    /// spent by 忌炎's ult instead of wasted, and nobody is swapped out mid-buff.
    /// Is this skill off cooldown for this role? Unlike IsCanUseSkill this also answers for a
    /// role that is not on the field, so the rotation can see what a team mate is holding.
    private static bool SkillOffCd(Entity entity, int skillId)
    {
        if (skillId == 0) return false;
        CharacterSkillComponent? skills = entity.GetComponent<CharacterSkillComponent>();
        return skills != null && skills.Valid && skills.GetSkillInfo(skillId) != null && !skills.IsSkillInCd(skillId);
    }

    /// Does this role have anything worth coming in for: a full ult, a skill off cooldown, or
    /// its echo.
    private static bool HasWork(Entity entity)
    {
        int role = RoleOf(entity);
        int ultA = 0, ultB = 0, skillA = 0, skillB = 0;
        if (role == Jiyan) { ultA = 1404304; ultB = 1404300; skillA = 1404201; skillB = 1404200; }
        else if (role == Mortefi) { ultA = 1204070; skillA = 1204021; skillB = 1204011; }
        else if (role == Shorekeeper) { ultA = 1505402; ultB = 1505401; skillA = 1505301; skillB = 1505302; }
        else return false;
        if (Full(entity, Aki.Protocol.EAttributeType.Energy, Aki.Protocol.EAttributeType.EnergyMax) &&
            (SkillOffCd(entity, ultA) || SkillOffCd(entity, ultB))) return true;
        if (SkillOffCd(entity, skillA) || SkillOffCd(entity, skillB)) return true;
        return EchoReady(entity);
    }

    /// Hands the field over. With a full concerto it follows the plan (守岸人 -> 莫特斐 -> 忌炎),
    /// because only then do the outro and the next role's intro fire — the intro is also what
    /// upgrades 守岸人's field. Without it, it still swaps to a team mate who is holding a skill
    /// while this role has nothing left to press: ok-ww swaps after every burst, and
    /// waiting for the concerto alone left every short fight to one character (R-75).
    private static bool TrySwitch(int role, bool concerto, bool field, long now)
    {
        SceneTeamModel? team = SceneTeamModel.Instance;
        if (team == null || team.GetChangeRoleCooldown() > 0.0) return false;
        Entity? shore = RoleEntity(Shorekeeper);
        Entity? mortefi = RoleEntity(Mortefi);
        Entity? jiyan = RoleEntity(Jiyan);
        // 守岸人 is worth the field when hers is gone or her ult is back; 莫特斐 when his ult is up.
        bool shoreFirst = shore != null && (!field || HasWork(shore));
        bool mortefiWork = mortefi != null && HasWork(mortefi);
        bool jiyanWork = jiyan != null && HasWork(jiyan);
        if (concerto)
        {
            if (role == Shorekeeper && (mortefiWork || mortefi != null) && SwitchTo(Mortefi)) return true;
            if (role == Mortefi && SwitchTo(Jiyan)) return true;
            if (role == Jiyan)
            {
                if (shoreFirst && SwitchTo(Shorekeeper)) return true;
                if (mortefiWork && SwitchTo(Mortefi)) return true;
            }
            if (role != Jiyan && SwitchTo(Jiyan)) return true;
            return false;
        }
        // Plain swap: only when this role is idle and someone else is not.
        WorldEntity? me = Me();
        if (me == null || HasWork(me)) return false;
        if (role != Shorekeeper && !field && shore != null && HasWork(shore) && SwitchTo(Shorekeeper)) return true;
        if (role != Mortefi && mortefiWork && SwitchTo(Mortefi)) return true;
        if (role != Jiyan && jiyanWork && SwitchTo(Jiyan)) return true;
        if (role != Shorekeeper && shore != null && HasWork(shore) && SwitchTo(Shorekeeper)) return true;
        return false;
    }

    private static void PlayRb(WorldEntity me, int role, long now)
    {
        bool concerto = Full(me, Aki.Protocol.EAttributeType.SpecialEnergy4, Aki.Protocol.EAttributeType.SpecialEnergy4Max);
        bool ultReady = Full(me, Aki.Protocol.EAttributeType.Energy, Aki.Protocol.EAttributeType.EnergyMax);
        bool forteFull = Full(me, Aki.Protocol.EAttributeType.SpecialEnergy1, Aki.Protocol.EAttributeType.SpecialEnergy1Max);
        bool field = RoleHasTag(Shorekeeper, TagField1) || RoleHasTag(Shorekeeper, TagField2) || RoleHasTag(Shorekeeper, TagField3);
        bool mortefiUlt = RoleHasTag(Mortefi, TagMortefiUlt);
        planNote = "rb " + role + (concerto ? " +concerto" : "") + (ultReady ? " +ult" : "") +
            (field ? " +field" : "") + (mortefiUlt ? " +coop" : "");

        if (role == Shorekeeper)
        {
            // The field first: everything else in the team scales off it.
            if (!field && ultReady && FirstReady(me, 1505402, 1505401) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);
                nextAction = now + 2500;
                return;
            }
            if (FirstReady(me, 1505301, 1505302) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.技能1);   // 混沌理论: heals and 5 蝶
                nextAction = now + 900;
                return;
            }
            if (EchoReady(me))
            {
                Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                nextAction = now + 1200;
                return;
            }
            // With a full concerto her outro lands on the next role; 莫特斐 first, so his ult and
            // its coordinated attacks are up before 忌炎 arrives. Idle without a concerto, she
            // still hands over rather than plain-attacking.
            if (TrySwitch(role, concerto, field, now))
            {
                nextAction = now + 900;
                return;
            }
            if (forteFull)
            {
                Hold(CSharpScript.Game.Input.EInputAction.攻击);   // 演绎: spends 5 段实论数据
                heavyUntil = now + 700;
                nextAction = now + 1400;
                return;
            }
            Tap(CSharpScript.Game.Input.EInputAction.攻击);
            nextAction = now + 350;
            return;
        }

        if (role == Mortefi)
        {
            if (ultReady && FirstReady(me, 1204070, 0) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);    // 浮翼狂想 for the whole team
                nextAction = now + 1800;
                return;
            }
            // 怒气值 full turns the skill into 怒火赋格; either way it is his best button.
            if (FirstReady(me, 1204021, 1204011) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.技能1);
                nextAction = now + 900;
                return;
            }
            if (EchoReady(me))
            {
                Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                nextAction = now + 900;
                return;
            }
            // His outro deepens the *next* role's heavy damage and dies if that role leaves, so
            // it always goes to 忌炎; idle without a concerto he hands over anyway.
            if (TrySwitch(role, concerto, field, now))
            {
                nextAction = now + 900;
                return;
            }
            Tap(CSharpScript.Game.Input.EInputAction.攻击);        // builds concerto and 怒气值
            nextAction = now + 350;
            return;
        }

        if (role == Jiyan)
        {
            // 破阵状态: every 普攻 is 重击破阵之枪. Nothing else is worth pressing.
            if (RoleHasTag(Jiyan, TagJiyanUlt))
            {
                Tap(CSharpScript.Game.Input.EInputAction.攻击);
                nextAction = now + 350;
                return;
            }
            // His ult is the payoff: spend it inside 守岸人's field, with 莫特斐's coordinated
            // attacks up if they are coming. Without a field it still beats holding it.
            if (ultReady && FirstReady(me, 1404304, 1404300) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.大招);
                nextAction = now + 1500;
                return;
            }
            if (FirstReady(me, 1404201, 1404200) != 0)
            {
                Tap(CSharpScript.Game.Input.EInputAction.技能1);   // 枪扫风定, stronger with 破阵值
                nextAction = now + 800;
                return;
            }
            if (EchoReady(me))
            {
                Tap(CSharpScript.Game.Input.EInputAction.幻象2);
                nextAction = now + 900;
                return;
            }
            // A heavy attack whenever 破阵值 is full, and while 莫特斐's coordinated attacks are
            // up (they also answer heavy hits) — but 破阵值 drains if he stops hitting, so the
            // chain in between is normal attacks.
            if (forteFull || (mortefiUlt && turnStep % 4 == 0))
            {
                Hold(CSharpScript.Game.Input.EInputAction.攻击);
                heavyUntil = now + 600;
                nextAction = now + 800;
                turnStep++;
                return;
            }
            // 忌炎 is the damage, so he keeps the field while the buffs are up; he leaves on a full
            // concerto (his outro pays the next role) or when he is idle and someone is holding a
            // skill. Plain attacks under 浮翼狂想 still call coordinated attacks, so an idle swap
            // is only worth it when nobody is buffing him either.
            if (concerto || !(field || mortefiUlt))
            {
                if (TrySwitch(role, concerto, field, now))
                {
                    nextAction = now + 900;
                    return;
                }
            }
            turnStep++;
            Tap(CSharpScript.Game.Input.EInputAction.攻击);
            nextAction = now + 350;
            return;
        }

        Tap(CSharpScript.Game.Input.EInputAction.攻击);
        nextAction = now + 400;
    }

    private static long heavyUntil;

    /// A held 攻击 (heavy attack) has to be let go of again.
    private static void ReleaseHeavy(long now)
    {
        if (heavyUntil == 0 || now < heavyUntil) return;
        heavyUntil = 0;
        Release(CSharpScript.Game.Input.EInputAction.攻击);
    }

    // ---- R-75: combat metering ------------------------------------------------------------
    //
    // cspatch inserts a call at the start of BaseDamageComponent.ExecDamageEvent, which runs for
    // every damage the client settles (its own and the server's reply), so this sees the real
    // numbers the player sees. A "fight" starts at the first damage dealt to a monster and ends
    // FightGapMs after the last damage. Reports (tab separated, appended):
    //   rb_fight.tsv  time, seconds, dealt, taken, kills, dps, per-genre dealt
    //   rb_kill.tsv   time, monster config id, seconds from its first hit, damage, dps
    // Live numbers are in auto_status.txt.
    private const long FightGapMs = 6000;
    private const int Genres = 13;         // ESkillGenre 0..11 plus "no genre"
    private const int Targets = 16;        // monsters tracked at once

    private static long fightStart;
    private static long fightLast;
    private static double fightDealt;
    private static double fightTaken;
    private static int fightKills;
    private static readonly double[] fightByGenre = new double[Genres];
    private static int fights;
    private static double lastFightDealt;
    private static double lastFightSeconds;

    private static readonly int[] targetEntity = new int[Targets];
    private static readonly string?[] targetName = new string?[Targets];
    private const int Sources = 8;
    private static readonly int[] sourceKey = new int[Sources];
    private static readonly double[] sourceDamage = new double[Sources];
    private static readonly long[] targetFirst = new long[Targets];
    private static readonly double[] targetDamage = new double[Targets];

    private static readonly string[] GenreName = new string[]
    {
        "普攻", "蓄力", "E技能", "大招", "QTE", "闪避反击", "地面闪避", "极限闪避",
        "被动", "战斗幻象", "探索幻象", "空中闪避", "无类别",
    };

    public static void OnDamage(BaseDamageComponent? victimComp, DamageResult? result, DamageEventInfo? inputParam, RequirementPayload? req)
    {
        try
        {
            if (victimComp == null || result == null) return;
            if (result.DamageData.CalculateType != (int)ECalculationType.Hurt) return;   // not a heal or a cost
            Entity? victim = victimComp.Entity;
            if (victim == null || !victim.Valid) return;
            CreatureDataComponent? vd = victim.GetComponent<CreatureDataComponent>();
            bool victimIsMonster = vd != null && vd.Valid && vd.IsMonster();
            // DamageResult.Damage is the change to the victim's life, so hurt is negative
            // (the game's own statistics negate it the same way for heals).
            double amount = result.Damage < 0f ? -result.Damage : result.Damage;
            long now = Environment.TickCount64;
            if (!victimIsMonster)
            {
                if (fightStart == 0) return;                        // only inside a fight
                fightTaken += amount;
                fightLast = now;
                return;
            }
            if (fightStart == 0)
            {
                fightStart = now;
                fightDealt = 0;
                fightTaken = 0;
                fightKills = 0;
                for (int i = 0; i < Genres; i++) fightByGenre[i] = 0;
                for (int i = 0; i < Targets; i++) targetEntity[i] = 0;
                for (int i = 0; i < Sources; i++) sourceKey[i] = 0;
            }
            fightLast = now;
            fightDealt += amount;
            fightByGenre[GenreIndex(req)] += amount;
            AddSource(req, amount);

            int slot = TargetSlot(victim.Id, now);
            if (slot >= 0)
            {
                if (targetName[slot] == null && vd != null) targetName[slot] = MonsterName(vd);
                targetDamage[slot] += amount;
                if (result.IsTargetKilled)
                {
                    fightKills++;
                    double seconds = (now - targetFirst[slot]) / 1000.0;
                    Append("rb_kill.tsv", DateTime.Now.ToString("HH:mm:ss") + "\t" + (targetName[slot] ?? "?") + "\t" +
                        seconds.ToString("0.00") + "\t" + ((long)targetDamage[slot]) + "\t" +
                        (seconds > 0.05 ? (long)(targetDamage[slot] / seconds) : 0) + "\r\n");
                    targetEntity[slot] = 0;
                }
            }
        }
        catch (Exception e)
        {
            lastError = "damage: " + e.Message;
        }
    }

    /// What to call this monster in the report: its config id when it has one, else the id the
    /// client's own logs use, else its name text id.
    private static string MonsterName(CreatureDataComponent data)
    {
        try
        {
            int roleId = data.GetRoleId();
            if (roleId > 0) return roleId.ToString();
            string? pb = data.EntityPbModelConfigId;
            if (pb != null && pb.Length > 0) return pb;
            string? tid = data.GetEntityTidName();
            return tid == null || tid.Length == 0 ? "?" : tid;
        }
        catch (Exception)
        {
            return "?";
        }
    }

    /// Damage per source: the skill id without its last three digits, which is the role for a
    /// role skill (1404…) and the echo or summon group otherwise. Stored as key + 1, so 0 is free.
    private static void AddSource(RequirementPayload? req, double amount)
    {
        int key = 0;
        if (req != null && req.SkillId.HasValue) key = req.SkillId.Value / 1000;
        for (int i = 0; i < Sources; i++)
            if (sourceKey[i] == key + 1)
            {
                sourceDamage[i] += amount;
                return;
            }
        for (int i = 0; i < Sources; i++)
            if (sourceKey[i] == 0)
            {
                sourceKey[i] = key + 1;
                sourceDamage[i] = amount;
                return;
            }
    }

    private static int GenreIndex(RequirementPayload? req)
    {
        if (req == null) return Genres - 1;
        System.Collections.Generic.IReadOnlyList<ESkillGenre>? genres = req.SkillGenres;
        if (genres == null || genres.Count == 0) return Genres - 1;
        int index = (int)genres[0];
        return (index >= 0 && index < Genres - 1) ? index : Genres - 1;
    }

    /// The slot of this monster, reusing the oldest one when the table is full.
    private static int TargetSlot(int entityId, long now)
    {
        int free = -1;
        int oldest = 0;
        for (int i = 0; i < Targets; i++)
        {
            if (targetEntity[i] == entityId) return i;
            if (targetEntity[i] == 0 && free < 0) free = i;
            if (targetFirst[i] < targetFirst[oldest]) oldest = i;
        }
        int slot = free >= 0 ? free : oldest;
        targetEntity[slot] = entityId;
        targetName[slot] = null;
        targetFirst[slot] = now;
        targetDamage[slot] = 0;
        return slot;
    }

    /// Called from the game tick: closes a fight once nothing has been damaged for FightGapMs.
    private static void CheckFightEnd(long now)
    {
        if (fightStart == 0 || now - fightLast < FightGapMs) return;
        double seconds = (fightLast - fightStart) / 1000.0;
        string line = DateTime.Now.ToString("HH:mm:ss") + "\t" + seconds.ToString("0.00") + "\t" +
            ((long)fightDealt) + "\t" + ((long)fightTaken) + "\t" + fightKills + "\t" +
            (seconds > 0.05 ? (long)(fightDealt / seconds) : 0);
        for (int i = 0; i < Genres; i++)
            if (fightByGenre[i] > 0) line += "\t" + GenreName[i] + " " + (long)fightByGenre[i];
        for (int i = 0; i < Sources; i++)
            if (sourceKey[i] != 0) line += "\t#" + (sourceKey[i] - 1) + " " + (long)sourceDamage[i];
        Append("rb_fight.tsv", line + "\r\n");
        fights++;
        lastFightDealt = fightDealt;
        lastFightSeconds = seconds;
        fightStart = 0;
    }

    private static void Append(string name, string line)
    {
        try
        {
            File.AppendAllText(Dir + name, line);
        }
        catch (Exception)
        {
        }
    }

    // ---- C# versions of the remaining join-scene JS patches (cspatch hooks these in) ----

    /// debug_disable.js: PositionPanel shows the player position (text 0) and a time line
    /// (text 4) in the corner; called at the end of every OnAfterTickBattleChildViewPanel.
    public static void HideTexts(UnrealEngine.UUIText? a, UnrealEngine.UUIText? b)
    {
        try
        {
            a?.SetUIActive(false);
            b?.SetUIActive(false);
        }
        catch (Exception)
        {
        }
    }

    /// R-156: the UID line (UidView, the personal card, the photo mark) shows this text instead of
    /// the player id. Win64/rb_uid.txt overrides it (first line); an empty file keeps the id.
    public const string DefaultUidText = "Tidelab";

    public static string? UidText()
    {
        try
        {
            string path = Dir + "rb_uid.txt";
            if (!File.Exists(path)) return DefaultUidText;
            foreach (string line in File.ReadAllLines(path))
                return line.Trim().Length == 0 ? null : line.Trim();
            return null;
        }
        catch (Exception)
        {
            return DefaultUidText;
        }
    }

    /// Called at the end of the method that wrote the player id into `text` with the text table
    /// entry `key` ("FriendMyUid", "UserId"): writes it again with UidText() as the argument.
    public static void SetUidText(UnrealEngine.UUIText? text, string key)
    {
        try
        {
            string? shown = UidText();
            if (text == null || shown == null) return;
            LguiUtil.Instance.SetLocalText(text, key, new object?[] { shown });
        }
        catch (Exception)
        {
        }
    }

    /// red_dot_remover.js: a red dot widget is hidden as soon as it registers (RedDotData.SetUiItem).
    public static void HideUiItem(UnrealEngine.UUIItem? item)
    {
        try
        {
            if (item != null && item.IsValid()) item.SetUIActive(false);
        }
        catch (Exception)
        {
        }
    }

    /// motorcycle.js: leave the vehicle before a teleport, like VehicleController.TryLeaveVehicle.
    public static void LeaveVehicle()
    {
        try
        {
            CharacterDriveVehicleComponent? drive = CSharpScript.Game.Global.BaseCharacter?.CharacterActorComponent?.Entity?.CheckGetComponent<CharacterDriveVehicleComponent>();
            Entity? vehicle = drive?.VehicleEntity;
            if (vehicle == null || !vehicle.Valid) return;
            vehicle.CheckGetComponent<BaseVehiclePerformComponent>()?.TryLeaveAllAtOnce(ELeaveVehicleType.Launch, "RBTeleport");
        }
        catch (Exception e)
        {
            lastError = "leave vehicle: " + e.Message;
        }
    }

    private static long nextCvarCheck;
    private static DateTime cvarFileTime;

    /// Called at the start of CSharpScript.Game.Game.Tick (cspatch). Every 2 s: when
    /// Win64/rb_cvars.txt changed, run each line as a console command (UE cvars such as
    /// a.URO.Enable 1; '#' starts a comment), the way the game's LowMemoryScalabilityController
    /// does. The C# build runs no JS, so this replaces frame_limit.js-style patches and lets
    /// engine settings be A/B tested without restarting. Result: rb_cvars_status.txt.
    public static void OnGameTick()
    {
        try
        {
            long now = Environment.TickCount64;
            CheckFightEnd(now);
            ReleaseHeavy(now);
            RunRotation(now);
            if (now < nextCvarCheck) return;
            nextCvarCheck = now + 2000;
            string path = Dir + "rb_cvars.txt";
            if (!File.Exists(path)) return;
            DateTime stamp = File.GetLastWriteTimeUtc(path);
            if (stamp == cvarFileTime) return;
            UnrealEngine.UWorld? world = GlobalData.World;
            if (world == null) return;
            cvarFileTime = stamp;
            string done = "";
            foreach (string raw in File.ReadAllLines(path))
            {
                string line = raw.Trim();
                int hash = line.IndexOf('#');
                if (hash >= 0) line = line.Substring(0, hash).Trim();
                if (line.Length == 0) continue;
                UnrealEngine.UKismetSystemLibrary.ExecuteConsoleCommand(world, line, null);
                done += line + "\r\n";
            }
            File.WriteAllText(Dir + "rb_cvars_status.txt", DateTime.Now.ToString("HH:mm:ss") + " applied:\r\n" + done);
        }
        catch (Exception e)
        {
            lastError = "cvars: " + e.Message;
        }
    }

    private static void ReadMode()
    {
        try
        {
            string path = Dir + "auto_mode.txt";
            if (!File.Exists(path))
            {
                mode = ModeDodge;
                return;
            }
            string text = File.ReadAllText(path).Trim();
            int space = text.IndexOf(' ');
            if (space > 0) text = text.Substring(0, space);
            if (text.Equals("off", StringComparison.OrdinalIgnoreCase)) mode = ModeOff;
            else if (text.Equals("counter", StringComparison.OrdinalIgnoreCase)) mode = ModeCounter;
            else if (text.Equals("base", StringComparison.OrdinalIgnoreCase) || text.Equals("team", StringComparison.OrdinalIgnoreCase)) mode = ModeBase;
            else if (text.Equals("rb", StringComparison.OrdinalIgnoreCase)) mode = ModeRb;
            else mode = ModeDodge;
        }
        catch (Exception e)
        {
            lastError = "mode: " + e.Message;
        }
    }

    private static void WriteStatus()
    {
        try
        {
            string fight = fightStart == 0
                ? "fight none (" + fights + " done, last " + ((long)lastFightDealt) + " in " + lastFightSeconds.ToString("0.0") + "s)"
                : "fight " + ((Environment.TickCount64 - fightStart) / 1000.0).ToString("0.0") + "s dealt " +
                  ((long)fightDealt) + " taken " + ((long)fightTaken) + " kills " + fightKills;
            string[] names = new string[] { "off", "dodge", "counter", "base", "rb" };
            File.WriteAllText(Dir + "auto_status.txt",
                "C# version " + Version + "\r\nmode " + names[mode] + "\r\ndodges " + dodges +
                "  counters " + counters + "  parries " + parries + "  switches " + switches +
                "\r\nplan " + planNote + "\r\n" + fight + "\r\n" +
                (lastError.Length > 0 ? "last error " + lastError + "\r\n" : ""));
        }
        catch (Exception)
        {
        }
    }
}
