# flapper

A control program for splitflap displays.

Some time ago I became fascinated by some projects that were recreating the old splitflap displays that used to be common in train stations and clock radios. The initial spark came from [this project](https://www.hackster.io/news/the-profan-o-matic-455-is-a-four-letter-split-flap-display-c5580ce94724), which is funny and probably cathartic, but the thing that stuck with me was the sound it made, so familiar and yet forgotten. If you were ever in Grand Central when the old departure boards built out of these things were still there, you'll remember the sound they made and the way each letter would appear out of the chaos of flipping characters.

The Profan-O-Matic used a [splitflap module design created by Scott Bezek](https://github.com/scottbez1/splitflap), and it's an impressive piece of work. If you're interested in building your own I recommend it, it's quite fun. I did, and eventually I ended up with 24 splitflap modules in a wooden case.

The latest versions of the project use the gRPC protocol for communication with the display.