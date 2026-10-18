# BetterSunshineModule research → ww-multiplayer

> Research note by Stephen, 2026-07-28, kept as written. It predates the modding-API plan; [docs/api/00-design.md](api/00-design.md) records how it feeds Phase E (a boot-time module loader in the style of Kuribo). Local paths and the "first experiment" section reflect that date.

Reference clone: `~/projects/reference-BetterSunshineModule`  
(Engine submodule: `lib/BetterSunshineEngine`)

Upstream:
- https://github.com/DotKuribo/BetterSunshineModule
- https://github.com/DotKuribo/BetterSunshineEngine (API headers)

## What BetterSunshineModule actually is

It is **not** a multiplayer API. It is a **template for Kuribo modules** that sit on top of BetterSunshineEngine (BSE).

Stack (top → bottom):

```
Your .kxe module  (BetterSunshineModule template)
        ↓ register callbacks / settings
BetterSunshineEngine.kxe  (shared runtime + high-level hooks)
        ↓ Kuribo loader
SMS DOL + JSystem + game types
```

Drop compiled modules into extracted ISO:

```
/files/Kuribo!/Mods/
  BetterSunshineEngine.kxe   # first
  _YourModule.kxe            # after (underscore sorts after)
```

## API surface that matters for multiplayer design

### 1. Lifecycle callbacks (the real gold)

From `include/BetterSMS/stage.hxx`:

| Callback | Signature | When |
|---|---|---|
| Stage init | `(TMarDirector*)` | Stage load |
| Stage update | `(TMarDirector*)` | Every frame (game logic) |
| Stage draw2D | `(TMarDirector*, J2DOrthoGraph*)` | HUD / 2D pass |
| Stage exit | `(TApplication*)` | Leave stage |

From `include/BetterSMS/player.hxx`:

| Callback | Signature | When |
|---|---|---|
| Player init | `(TMario*, bool)` | Player create |
| Player loadAfter | `(TMario*)` | After load |
| Player update | `(TMario*, bool)` | Per player tick |
| State machine | `(TMario*) → bool` | Specific Mario state |
| Collision handler | `(TMario*, TBGCheckData*, flags)` | Contact types |

**Pattern:** engine owns the hook sites; modules only **register** functions. Multiple modules can compose without fighting over the same branch.

### 2. Low-level patch macros

```cpp
SMS_PATCH_B(address, function);   // branch
SMS_PATCH_BL(address, function);  // branch-and-link
SMS_WRITE_32(address, value);
```

Used when callbacks aren't enough. Same idea as Freighter `hook_branchlink` in our inject path.

### 3. Module registration + settings

```cpp
static BetterSMS::ModuleInfo sModuleInfo("Demo Module", 1, 1, &sSettingsGroup);

KURIBO_MODULE_BEGIN("OurModule", "JoshuaMK", "v1.0") {
    KURIBO_EXECUTE_ON_LOAD { initModule(); }
}
KURIBO_MODULE_END()
```

Settings groups can save to memory card (banner/icon blobs in the demo `main.cpp`). Multiplayer-relevant: lobby options, sync toggles, debug flags — in-game, not only host CLI.

### 4. Player extension bag

```cpp
Player::registerData(TMario*, key, void*);
Player::getRegisteredData(TMario*, key);
```

Per-player arbitrary data keyed by string. For multiplayer that would be remote puppet metadata, net ID, last pose seq, etc. — **without** hardcoding into `TMario`.

Also: `mIsMultiplayerStage` already exists on `TStageParams` (stage.hxx). BSE anticipates multiplayer stages at the config level even though BSMSO is closed-source.

### 5. Typed game headers

Modules include real types:

```cpp
#include <SMS/Player/Mario.hxx>
#include <SMS/System/MarDirector.hxx>
#include <JSystem/J2D/...>
```

That's the biggest SMS advantage: **compile against decomp-like headers**, not only raw addresses.

## How ww-multiplayer maps today

| BSE concept | ww-multiplayer equivalent |
|---|---|
| Kuribo module load | Freighter DOL T2 section + ISO patch (`inject/`) |
| `KURIBO_EXECUTE_ON_LOAD` | `main01_init` → installs `multiplayer_update` callback ptr |
| Stage update callback | `frame_shim` / `multiplayer_update` (hooked `bl`) |
| Stage draw callback | `daPy_draw_hook` at `0x80108210` |
| Settings UI | Go CLI flags / mailbox debug bytes |
| `Player::registerData` | `Mailbox` + `Puppet` / pose slots at `0x80412F00` |
| High-level Mario API | Manual offsets + `game.h` decls + zeldaret/tww |
| Multi-module compose | Single inject blob (all features co-located) |

**Since then (v0.4.0, 2026-10-17):** the C side is a core (`inject/src/core`: hooks, services, the call order fixed in one dispatcher) plus the multiplayer C mod (`inject/src/mp`; docs/api §6.6). `game.h` became `inject/include/wwapi.h`. Settings live in the desktop app (lobby, Mods page) and in each mod's `mod.json`, with the `mods` command. The Go features are mods on `wwapi/` (`mods/multiplayer`, `mods/coop`). The table above is as of 2026-07-28.

We already have the **host/guest split** BSE doesn't provide:

```
Dolphin + patched DOL (guest C)
        ↕ mailbox @ 0x80412F00
ww-multiplayer Go host (net + pose + controls)
```

BSE is all **in-process**. Our mailbox is the surrogate for "exported guest API."

## What to steal (concrete)

### Near-term (no Kuribo needed)

1. **Callback table discipline**  
   Treat `multiplayer_update` / `daPy_draw_hook` as registrars, not feature dumps. Split features into functions registered from `main01_init` (even if the "registry" is a static array of fn pointers). Matches BSE compose model.

2. **Stable guest API surface**  
   Freeze a small contract in `mailbox.h` / docs:
   - pose publish (local → host)
   - pose consume (host → puppet slots)
   - face state
   - stage/room IDs (add if missing)
   - puppet active/hide
   - warp / input (already present)

   BSE's win is stability; our mailbox has grown organically with diagnostics. **Version the mailbox** (`u32 api_version` at +0x00 or a reserved field) so Go and C fail soft.

3. **Use tww headers more like SMS headers**  
   Prefer `#include` from a vendored `zeldaret/tww` tree (or generated struct headers) over magic offsets in comments. Same mental model as `#include <SMS/Player/Mario.hxx>`.

4. **Stage-aware hide**  
   BSE stage callbacks know when stages load/exit. We should publish `stage_id` / `room_id` every frame in the mailbox and have Go hide remote puppets on mismatch (known limit in README).

### Medium-term (optional infrastructure)

5. **Thin "BetterWindEngine" layer**  
   Not full Kuribo. Just:
   - one Freighter inject that only installs hooks + a callback list
   - multiplayer features as separate C translation units registered at init
   - host still owns TCP

6. **Don't chase Kuribo for WW multiplayer v1**  
   BSMSO still uses an external launcher. Networking outside the game is correct. Guest code should stay thin: render puppets, publish pose, honor room gates.

## Demo module structure (BSE) in one glance

```cpp
// settings + ModuleInfo
// onStageInit / onStageUpdate / onStageDraw2D
// initModule() { register callbacks + settings }
// KURIBO_MODULE_BEGIN ... ON_LOAD { initModule(); }
```

Equivalent shape for us:

```c
// mailbox.h = public ABI (versioned)
// multiplayer_update = stage-update analogue
// daPy_draw_hook = draw analogue
// main01_init = module load
// Go host = networking + CLI "settings"
```

## Local paths

| Path | Role |
|---|---|
| `C:\Users\4step\projects\ww-multiplayer` | This repo |
| `C:\Users\4step\projects\reference-BetterSunshineModule` | BSM template + BSE submodule |
| `C:\Users\4step\Desktop\Dolphin-x64` | Dolphin install |

## Suggested first experiment after ROM lands

1. `ww-multiplayer.exe patch <rom.iso>`
2. Boot patched ISO in Desktop Dolphin, load save on Outset
3. `ww-multiplayer.exe debug` — confirm Link position reads
4. Local 2-player harness if available (`wwdev mp-local` / host+fake-client)

Research-only; no code changes required to benefit from the callback/versioning mindset above.
