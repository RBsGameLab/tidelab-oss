# Specs

Behaviour specs for tidelab: what the code implements. Each spec is updated in the same change as
the code it describes.

| Spec | Covers |
|---|---|
| [network.md](network.md) | UDP handshake, KCP settings, frames, cipher and key exchange, login on the gateway, reconnect, one session per player |
| [login.md](login.md) | HTTP login server |
| [config.md](config.md) | HTTP config and CDN server |
| [panel.md](panel.md) | the content panel: overrides over the automatic activity rules (edition, on/off, window), the activity plan, "a new window is a new run", the local page and its API |
| [save-format.md](save-format.md) | every file under `state/` |
| [data-tables.md](data-tables.md) + [data-tables.tsv](data-tables.tsv) | the config tables the server loads, the fields it reads, value shapes, tables outside ConfigDB, binary cache format |
| [enter-world.md](enter-world.md) | from `EnterGameResponse` to standing in the world: login, reconnect and new-account pushes in order, `JoinSceneNotify`, the requests the client sends while loading |
| [entities.md](entities.md) | net ids and the id floor, `EntityPb` and components per kind, positions (feet + `HalfHeight`), state tags, role entities, client-made entities, add/remove, what is saved, typed `ComponentsData` |
| [spawning.md](spawning.md) | the world spawner: candidate passes, radii and caps, refresh steps, `world_net_ids`, login/reconnect clean-up, the robot `npcs` check |
| [movement.md](movement.md) | `MovePackagePush`, position saving, beacon teleports, world-instance `EnterInstRequest`, scene change, vehicles |
| [roles.md](roles.md) | new-account roles, login role list and pushes, role attribute formulas, entity initial buffs/skills, role requests, `Buff.GameAttributeID` validation |
| [inventory.md](inventory.md) | unique-id counter, normal-item snapshot+log and `ItemInfo` filter / stray-weapon migration, weapons, echoes, presets, calabash, projector |
| [formation.md](formation.md) | `formation_info`, login/scene-join sync, 3.8 `UpdateSceneGroupDataNotify`, formation requests including reorder, role entity diff and `SwitchRoleRequest` |
| [combat.md](combat.md) | client vs server combat boundary, combat pack dispatch, monster attributes & HP, damage/heal, fall damage, kills and drops, death/revive/beacon heal, buffs & timers, combat relays, AI FSM |
| [quests.md](quests.md) | quest engine, quest requests (`QuestAcceptRequest`..`QuestTrackRequest`), world interaction (`EntityInteractRequest`, `EntityDynamicInteractRequest`, `InteractOptionRequest`, `SplineMoveRequest`), level-play/action replies, runtime hooks into login/move/heartbeat/kill/spawn |
| [flows.md](flows.md) | dialogue/story flows (`FlowStartNotify`, `FlowEndRequest`, `FlowActionRequest`), `TeleportNotify` (`FlowAction`), flow queue ordering |
| [dungeons.md](dungeons.md) | instance dungeons (`EnterInstRequest`, `LeaveInstRequest`, `ReChallengeRequest`, `DungeonEntranceInfoRequest`), Adversity Tower (`TowerRequest`, `TowerRecommendRequest`, formation swap/restore), prefab/spawner rules, dungeon combat rules |
| [shops.md](shops.md) | NPC & pay shops (`ShopInfoRequest`, `ShopBuyRequest`, `PayShop*`, `shop_bought` format), synthesis/cooking/forging/material-replace/item-exchange (14 requests), gacha (`GachaInfoRequest`, `GachaRequest`, rate constants) |
| [activities.md](activities.md) | activities (`ActivityRequest`, 85 excluded ids, `Task`, role rewards, scratch card & turntable), boss rush, lord gym, fishing, mailbox (`MailInfosNotify`, heartbeat `MailAddNotify`), dream link |
| [systems.md](systems.md) | achievement, client storage, area terminal, map marks, login pushes, adventure manual & Sola guide, world map & custom marks, explore tools & projector summons, guides, profile, music, chat & friends, cosmetics/skins, camera, motorcycle & ride-sharing, tutorials/fixed UI replies, chat/gift-mail/`cmd.txt` commands |
| [audit.md](audit.md) + [combat-client-calls.tsv](combat-client-calls.tsv) | the audit after a play session: server log stamps and archive, the checks (unanswered combat requests, stuck monsters, empty replies, dropped pushes, error replies, warnings, client errors), report, noise list; the combat requests the client waits on |
| [client-patch.md](client-patch.md) | the launcher (`tidelab_start.exe`), DLL entry (`tidelab_patch.dll`), inline hook requirements, runtime files beside the DLL, CPU load limiter, sampling profiler, and clean exit watch |

Conventions:

- Wire message and field names follow the client's protocol schema. Integers on the wire are
  little-endian unless a spec says otherwise.
- Every spec ends with the tests its subsystem needs.
