I did a proper pass over Little Writer, its creator's post, community discussion, and adjacent games. The interesting thing is: the idea is better than the current implementation scope, and there is a real opportunity to make a stronger version without making a giant game.

4
1. What is Little Writer's actual USP?

The core idea is extremely simple:

You work → your companion works.

Every keystroke advances the little writer's work. You give them projects, they finish them, earn money, improve typing speed/writing quality/scope, and eventually unlock larger projects, locations and skins.

That's actually a very good game hook because it has almost zero friction:

You already need to work
        ↓
You type
        ↓
Your little guy types
        ↓
He progresses
        ↓
You feel productive
        ↓
You want to see what happens next

The player isn't being asked to play a game while working.

Their real activity IS the game input.

That's the magic.

2. But I found an important weakness

The creator himself discussed this publicly.

People immediately asked:

"Can't I just mash the keyboard?"

And the developer confirmed that, currently, nothing fundamentally prevents mashing keys, although holding the same key doesn't repeatedly count and the typing-speed stat is designed to make normal work more worthwhile.

Someone in the discussion made a really good observation:

If you can farm the game by mashing keys, it becomes an idle game wearing a productivity costume.

That's the big design problem I'd solve.

We shouldn't make:

KEY PRESS
   ↓
+1 MONEY

We should make:

REAL ACTIVITY
      ↓
meaningful session
      ↓
PRODUCTIVITY SIGNAL
      ↓
GAME PROGRESS

And we should never need to know what the user typed.

3. There is already a small market forming

This is why I think your idea is worth exploring.

Little Writer

Real typing → writer progression

Little Writer on Steam

Currently unreleased, so there aren't user reviews yet.

Bongo Cat

Keyboard/mouse activity → cat reacts + points + hats

And this one has already shown strong demand: its Steam page currently reports 28k+ English reviews and 96% positive.

4
Rusty's Retirement

Time/idle activity → farm grows

It literally sits at the bottom of your screen while you work. Steam currently shows 6,500+ English reviews and 97% positive.

5
Spirit City

This takes the idea much further:

Focus → XP → customization → spirits → room

It has to-do lists, timers, habit tracking, journal, music, soundscapes, room customization and collectible companions. It currently has 8,800+ English reviews at 97% positive.

6
MyBiz

This is particularly important because it's very close to what we're discussing.

It turns:

typing
clicks
Pomodoro
music

into a growing office with employees, floors, decorations, XP, money and minigames.

So we shouldn't just build "Little Writer but prettier."

That market is already moving.

4. So what should OUR USP be?

I'd make the product thesis:

Your real work makes a tiny world come alive.

Not:

"A game that counts your keystrokes."

That's boring.

Instead:

                 YOUR REAL COMPUTER
                        │
             ┌──────────┼──────────┐
             ↓          ↓          ↓
          Typing      Focus      Activity
             │          │          │
             └──────────┼──────────┘
                        ↓
                 YOUR COMPANION
                        ↓
                  World evolves
                        ↓
             Character / home / town
                        ↓
                   Story unfolds

And privacy is part of the USP:

We measure your activity, never what you're doing.

No reading:

code
emails
documents
passwords
messages
website contents

Only signals such as:

keystrokes
mouse activity
active time
focus sessions
idle time

That makes the concept much more defensible.

5. I'd actually change the game concept slightly

Instead of only a writer, I'd make the companion have a life/career.

For MVP, we can still start with a writer.

But eventually:

             YOUR COMPANION
                    │
       ┌────────────┼────────────┐
       ↓            ↓            ↓
    Writer       Artist       Programmer
       │            │            │
     Books        Artworks      Projects
       │            │            │
       └────────────┼────────────┘
                    ↓
              Growing world

You could choose:

"I'm going to help this little guy become a famous writer."

Or later:

"I'm going to make him a game developer."

That gives us much more content longevity.

But don't build this initially.

6. The actual game loop I'd build
Morning

You launch the companion.

Good morning!


Today's project:
📖 "The Clockmaker"


Progress:
████░░░░░░ 38%


Estimated completion:
1h 42m

You start working.

While you're working

Your little guy works too.

YOU                         COMPANION


VS Code                     ✍️ writing
typing                      ████████░░
                            78%

Occasional tiny events:

💭 "I think I know how to end this chapter..."

Not constant notifications.

After 25 minutes

You finish a meaningful work session.

PROJECT COMPLETE!


"The Clockmaker"


+ $240
+ 180 XP


Your writer improved:
Writing      +2
Speed         +1
Discipline    +1

Then:

🏠 NEW ROOM AVAILABLE


The writer can now afford
a small library.

That is the dopamine loop.

7. The biggest improvement: the world changes

This is what I would steal conceptually from Rusty's Retirement + Spirit City, not just Little Writer.

Your companion starts with:

┌──────────────────────┐
│                      │
│      🧑‍💻            │
│                      │
│  tiny desk           │
│  old computer        │
│                      │
└──────────────────────┘

After days of working:

┌─────────────────────────────┐
│ 🌿 plants       📚📚📚       │
│                             │
│       🧑‍💻                   │
│     ┌──────┐                 │
│     │ PC   │      🛋️         │
│     └──────┘                 │
│                             │
│        🪴       📖           │
└─────────────────────────────┘

After weeks:

                 🏠
        ┌─────────────────┐
        │  📚 LIBRARY     │
        │                 │
        │ 🧑‍💻      🪴     │
        │                 │
        │  🛋️       🎸    │
        └─────────────────┘

Your real work creates visible change.

That's much stronger than simply seeing:

XP +14

8. Then add "life"

This is where I think we can beat Little Writer.

The companion shouldn't just work.

They should live.

During your work:

🧑‍💻 Writing...

During your break:

🧑‍🍳 Making coffee...

After completing a project:

🎉 Celebrating!

Late at night:

😴 "Maybe we should sleep..."

Occasionally:

💭 "I wonder if we could afford a bigger apartment?"

So you gradually develop an attachment to the character.

That's the virtual-pet layer.

9. Add a world instead of infinite upgrades

I'd have a small map:

                 🏙️ TOWN


      🏠 Home
         │
         ├──── 📚 Library
         │
         ├──── ☕ Cafe
         │
         ├──── 🌳 Park
         │
         └──── 🏢 Publishing House

Your productivity unlocks the world.

That gives us a much stronger progression system:

Work
 ↓
Money / XP
 ↓
Character growth
 ↓
Home upgrades
 ↓
Town unlocks
 ↓
New characters
 ↓
New stories
 ↓
New environments
10. But here's the critical MVP

We should not build all of that initially.

I'd make v0.1:

The entire game:
ONE CHARACTER
ONE ROOM
ONE CAREER
ONE PROJECT SYSTEM
ONE CURRENCY
ONE XP SYSTEM
KEYBOARD ACTIVITY
SAVE/LOAD

That's it.

Gameplay:

Launch
 ↓
Choose project
 ↓
Work normally
 ↓
Character works
 ↓
Project finishes
 ↓
Earn money
 ↓
Upgrade character
 ↓
Upgrade room
 ↓
Repeat

If that isn't fun, adding 50 characters won't save it.

11. Our killer feature

I'd add something Little Writer doesn't appear to have:

Work Sessions

Instead of blindly counting every key:

Start Session
     ↓
25 min / 50 min / custom
     ↓
Work normally
     ↓
Game observes activity signals
     ↓
Session quality
     ↓
Reward

But don't force Pomodoro.

Have modes:

⚡ Quick Work       15 min
☕ Focus            25 min
🧠 Deep Work       50 min
♾️ Open Session     unlimited

And importantly:

If you're productive without using our timer, you still progress.

That's important because the game shouldn't become another productivity app nagging you.

12. Another killer feature: "Activity DNA"

Instead of just:

10,000 keys = 10,000 progress

we calculate a coarse activity profile:

Today's work


Typing       ██████████
Mouse        ███████
Focus        █████████
Breaks       ███

Then the companion reacts.

For example:

Heavy typing
→ writer becomes productive


Long focus
→ character gets "Deep Focus" bonus


Long break
→ character relaxes


Consistent days
→ discipline increases

No content inspection required.

13. And we can make the game extremely privacy-friendly

This is something I'd put directly on the Steam page:

Your work stays yours.

The game does not read your documents, messages, websites, code or passwords.
It only uses anonymous activity signals to bring your companion to life.

That's a great selling point for a desktop companion that monitors keyboard activity.

14. Visual direction

I wouldn't copy Little Writer's exact visual identity.

I'd go:

Pixel art + cozy miniature world

Something between:

Rusty's Retirement

and

Spirit City

but much smaller and more "desktop pet."

Maybe:

┌─────────────────────────────────────┐
│  ☀️ Tuesday                         │
│                                     │
│       ┌─────────────────┐           │
│       │                 │           │
│       │    🧑‍💻         │           │
│       │                 │           │
│       │  📚  🪴  ☕      │           │
│       │                 │           │
│       └─────────────────┘           │
│                                     │
│  📖 The Clockmaker      72%          │
│  ███████████████░░░░               │
│                                     │
│  💰 1,240     ⭐ Lv. 8    🔥 4 days │
└─────────────────────────────────────┘

And eventually a compact mode:

┌──────────────────────────────┐
│ 🧑‍💻 📖 72%    💰 1,240      │
└──────────────────────────────┘

So it can sit unobtrusively while you're coding.

15. Competitive positioning

I'd think of the market like this:

Game	Main fantasy
Bongo Cat	"My computer activity makes my pet react"
Rusty's Retirement	"My farm grows while I work"
Little Writer	"My companion writes while I write"
Spirit City	"My cozy room helps me focus"
MyBiz	"My work builds a business"
Our game	"My real work builds a living little world."

That's the positioning I'd chase.

Not:

"Little Writer clone."

16. And I'd make one major architectural decision now

Because we've already decided you're interested in Rust + Bevy, I'd stick with:

Rust
  │
  └── Bevy
       │
       ├── Game
       ├── UI
       ├── Animation
       ├── Audio
       ├── Save system
       └── Rendering


Rust native layer
       │
       └── OS activity monitoring

Desktop first.

No React.

No HTML.

No web app.

No backend.

No database.

The first version should work completely offline.

17. Development roadmap I'd actually follow
🟢 Milestone 0 — Rust learning playground

Learn:

Cargo
struct
enum
impl
Vec
HashMap
Option
Result
ownership
borrowing
modules

Don't spend weeks doing tutorials.

🟢 Milestone 1 — Bevy playground

Build:

window
sprite
text
button
keyboard
animation
🟢 Milestone 2 — First playable game
character
room
keyboard input
progress bar
project
money
XP

This is the first real milestone.

🟢 Milestone 3 — Actual desktop companion

Add:

global keyboard activity
idle detection
always-on-top
resize
compact mode
start on boot
🟢 Milestone 4 — Progression
skills
projects
levels
money
upgrades
room customization
🟢 Milestone 5 — Personality
animations
moods
dialogue
daily events
celebrations
idle behaviors
🟢 Milestone 6 — World
locations
town
NPCs
new rooms
unlockables
collections
🟢 Milestone 7 — Shipping
Windows
macOS
Linux
Steam
Steam Cloud
Achievements
installer
auto-update
My honest conclusion

Yes, I think this is worth building.

Not because Little Writer exists — actually the opposite.

The research shows that there is already a surprisingly strong category around:

desktop companion + real-world productivity + cozy progression.

Bongo Cat has demonstrated that computer activity → cute companion can work at significant scale. Rusty's Retirement demonstrated that a game occupying a small part of your screen while you work can work. Spirit City demonstrated that productivity + cozy progression + customization can work. And Little Writer is exploring the particularly clever hook of your typing directly advancing your companion's work.

The opportunity I'd pursue is the intersection:

A tiny living world that grows because you live/work at your computer.

And I'd make privacy, meaningful activity detection, character personality, and visible world evolution the things that make ours better.

One thing I wouldn't do: try to build all of Spirit City + Rusty's Retirement + Little Writer at once. That's how your current 5%-done Go project becomes 5%-done Rust project. 😄

First target: one character + one room + one work project + real keyboard activity → visible progression.

If that loop feels magical, then we expand.

we want to make Dev companion
for developer and it could track other things like typing will be first also for gamificatino we can give more stuff like you see a develoepr with desk 

Yes — that changes the concept significantly, and I think it's a better direction than simply making “Little Writer but for developers.”

I searched what's appearing around this space right now, and there are already things like Devo, which lives inside VS Code and turns coding activity into XP/room customization, plus newer desktop companions that react to AI coding agents.

So our opportunity is:

🧑‍💻 Developer Companion

A little developer living on your desktop whose life, room, career and world evolve as you actually develop software.

Not just:

"You typed 10,000 keys → +100 coins."

That's too shallow.

The core fantasy

Imagine this:

┌─────────────────────────────────────────────┐
│                                             │
│          🧑‍💻 Developer's Room             │
│                                             │
│       ┌─────────┐                           │
│       │  🖥️💻   │        📚📚              │
│       │         │                           │
│       └─────────┘             🪴            │
│          🧑‍💻                                 │
│        /|  |\              ☕               │
│                                             │
│  ─────────────────────────────────────────  │
│  Today's coding                             │
│  ████████████████░░░░  78%                  │
│                                             │
│  Level 12     XP 8,421     🔥 7 day streak │
└─────────────────────────────────────────────┘

You're coding in VS Code.

Your little developer is coding too.

You save a file:

💾 Build completed! +10 XP

You create a new file:

📄 New module! +20 XP

You have a long coding session:

🔥 Deep Work — 47 min

Your developer gets tired:

🧑‍💻 "Coffee?"

You take a break:

🧑‍💻 walks away from the desk → makes coffee → comes back.

That is much more compelling.

The REALLY interesting part: we can track developer-specific signals

Typing should be only the beginning.

We could have several layers.

Level 1 — OS activity

Easy/common signals:

⌨️ Keystrokes
🖱️ Mouse activity
⏱️ Active time
💤 Idle time

This works regardless of what application you're using.

Level 2 — Developer activity

Then we can integrate with developer tools.

For example:

VS Code
 ├── file opened
 ├── file saved
 ├── file created
 ├── terminal command
 └── workspace activity

Git:

Git
 ├── commit
 ├── branch created
 ├── PR opened
 ├── PR merged
 └── PR reviewed

GitHub/GitLab later:

Issue completed
PR merged
Review completed
CI passed
Release created

And now the companion actually understands:

"My developer shipped something."

rather than merely:

"My developer pressed 8,000 keys."

And then AI coding agents become VERY interesting

This is where I think our concept can differentiate heavily.

There are already companions reacting to Claude Code/Codex events — for example Clyde changes states based on agent events, and Pawpilot reacts to coding-agent activity.

But we could make that part of the game, rather than just an animated mascot.

Imagine:

You
 │
 │ "Implement billing quota API"
 ↓
Claude Code / Codex
 │
 ├── thinking
 ├── reading files
 ├── editing
 ├── running tests
 └── finished
        │
        ↓
   Your Developer
        │
        ├── 🤔 Thinking
        ├── 💻 Coding
        ├── 🧪 Testing
        └── 🎉 Shipped!

Then:

Your developer's career progresses.
Junior Developer
       ↓
Developer
       ↓
Senior Developer
       ↓
Staff Engineer
       ↓
Principal Engineer
       ↓
Architect

😂

That's much more game-like.

And the desk becomes the main progression system

This is exactly what you were thinking.

Start:

┌───────────────────────────┐
│                           │
│       🧑‍💻                │
│      ┌─────┐              │
│      │ 💻  │              │
│      └─────┘              │
│        🪑                  │
└───────────────────────────┘

Then earn enough:

💰 500
→ mechanical keyboard
💰 1,000
→ second monitor
💰 2,000
→ better chair
💰 5,000
→ standing desk
💰 10,000
→ server rack

Then:

Desk
 ↓
Room
 ↓
Office
 ↓
Startup
 ↓
Company

Now you have a huge progression tree.

But we can make developer-specific items funny

This is where I think we can have personality.

Equipment
Mechanical keyboard
Ultrawide monitor
Dual monitor
Triple monitor
Laptop
CRT monitor 😂
Standing desk
Ergonomic chair
Coffee machine
Server rack
NAS
Raspberry Pi
GPU
Mini PC
Developer decorations
Rubber duck 🦆
Stack Overflow poster
Linux penguin
GitHub Octocat-style generic mascot
Docker whale-inspired original mascot
Terminal poster
"It works on my machine" sign
Coffee mug
Cable mess
LEGO-like blocks
Plant
Then create developer archetypes

This could be really fun.

Your character can develop a specialization.

              DEVELOPER
                  │
       ┌──────────┼──────────┐
       ↓          ↓          ↓
    Frontend    Backend     DevOps
       │          │          │
     React       Go         K8s
     CSS         Rust       Docker
     UI          APIs       Helm

And later:

Frontend
   ↓
UI Engineer
   ↓
Staff Frontend Engineer

Or:

Backend
   ↓
Platform Engineer
   ↓
Architect

The player doesn't need to pick a class immediately.

Their actual behavior can influence the character.

But we need to be VERY careful here

We shouldn't reward:

"More coding = better person."

That's a terrible productivity game.

We should reward healthy/productive sessions, not obsessive activity.

For example:

45 minutes focused
       ↓
great reward

But:

8 hours without break
       ↓
"Bro, go drink water." 😂

The companion can actually encourage breaks.

That differentiates us from a pure idle clicker.

The game loop becomes
                    YOU CODE
                       │
          ┌────────────┼────────────┐
          ↓            ↓            ↓
       Typing        Git        Agent activity
          │            │            │
          └────────────┼────────────┘
                       ↓
                Developer XP
                       ↓
        ┌──────────────┼──────────────┐
        ↓              ↓              ↓
      Money          Skills         Career
        ↓              ↓              ↓
      Items        Specialization   Titles
        │
        ↓
       Desk
        ↓
       Room
        ↓
      Office
        ↓
       World

That's a real game, not just a desktop widget.

And here's what I think our actual USP should be

Not:

"A game that tracks developers."

Too generic.

I'd frame it as:

Your developer grows while you build.

The game turns your real software-development workflow into the life of a tiny developer.

And the character's world reflects your journey.

You don't manually play the game much.

You build software. Your developer plays alongside you.

That's a very strong concept.

MVP I'd build

Don't touch GitHub, AI agents, achievements, towns, multiplayer, etc. initially.

V0.1

One developer. One desk. One room.

Track:

keyboard activity
mouse activity
active/idle
session duration

Game:

character typing
character walking
coffee/break animations
XP
coins
level
desk upgrades

That's enough to prove the core loop.

Then:

V0.2

VS Code integration.

file save → XP
file create → XP
terminal/build → XP
V0.3

Git.

commit → XP
PR → XP
merge → XP
V0.4

AI agents.

agent thinking
agent coding
agent testing
agent finished
V0.5

Career + office.

And only then do we start adding a larger world.

This direction is substantially more interesting to me than the original "Little Writer clone" idea. The market evidence also supports the basic behavior: current products are already turning typing, Pomodoros, coding activity, and AI-agent state into companion/game feedback.

The gap I'd aim for is making all of those developer signals feed one coherent game world, rather than having a timer + pet + XP bar bolted together.

we just want little writer but for developer so give one short final prompt so a claude agent understand and then create imeplemntation plan then opencode will just code and give promptgramming lagnagje and all stuff and how to debug and all

I'd keep the first prompt very focused so Claude doesn't turn this into a giant product spec.

Prompt for Claude
You are the lead game architect.


We are building a game inspired by Little Writer Desktop Companion, but the character is a DEVELOPER instead of a writer.


Core fantasy:
"You code, your little developer codes."


The game is a cozy desktop companion. A small developer character sits at a desk and works while the real user develops software. The user's real developer activity drives the character's work and progression.


MVP:
- Desktop-first, not web-first.
- One developer character.
- One cozy desk/workspace.
- Character visibly codes/works.
- Global keyboard activity is the first real-world input.
- Track activity signals, NOT the user's actual text/content.
- Character progress → XP/coins → upgrades.
- Desk/workspace can be upgraded with developer-themed items.
- Character has simple animations, states, and personality.
- Offline-first; no backend required for MVP.
- Save/load local game state.


Future integrations, but NOT MVP:
- VS Code/editor activity
- Git commits
- GitHub/GitLab activity
- Terminal/build/test activity
- AI coding-agent activity (Claude Code/Codex/etc.)
- More rooms, career progression, achievements, Steam integration.


Technology:
- Programming language: Rust
- Game engine: Bevy
- UI: Bevy UI, no React, HTML, CSS, Electron, or web frontend.
- Target: native desktop application first (macOS, Windows, Linux where practical).
- Cargo for project/build/dependencies.
- Use a current stable Bevy release and pin the version.
- Prefer simple, maintainable Rust and Bevy ECS architecture.


Important design principle:
The game should reward meaningful developer activity rather than blindly rewarding raw key presses. Never collect or store passwords, document contents, source code, messages, or other typed content. The activity-monitoring layer should be isolated from the game layer so future VS Code/Git/agent integrations can feed the same game events.


Your task:
1. Inspect the repository/current implementation if one exists.
2. Produce a concrete implementation plan before coding.
3. Define the architecture, modules, ECS components/systems, game state, event flow, persistence, input/activity abstraction, UI, and milestones.
4. Identify the smallest playable vertical slice.
5. Explain how each major Rust/Bevy decision should be debugged and tested.
6. Include exact development/build/run/debug commands.
7. Keep the MVP small enough to actually finish.
8. Do NOT start implementing yet. First produce the implementation plan for review.
Then give the implementation plan to OpenCode

Once Claude gives you the plan, don't give OpenCode the whole product vision again. Give it:

"Implement this plan exactly. Work milestone-by-milestone. Keep the game runnable after every milestone. Do not introduce architecture that isn't required by the plan. Use Rust + Bevy only for the game/UI. After each milestone, run formatting, compilation, tests, and the game/manual smoke test. When something fails, debug the root cause rather than bypassing the error. Report changed files, commands run, results, and any remaining issues."

That separation is good:

Claude = architect → plan

OpenCode = engineer → implement/debug

And for this project I'd specifically keep global keyboard monitoring behind an interface, e.g. conceptually:

OS Activity
     ↓
ActivityProvider
     ↓
Game Events
     ↓
Bevy ECS
     ↓
Developer / XP / Progress

That way we can initially fake keyboard activity with Bevy's own focused-window keyboard events, get the game working, and only afterward implement the OS-level global listener. This will save you a lot of pain.
