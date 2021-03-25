# Dagger vs. Other Software


## Dagger vs. PaaS (Heroku, Firebase, etc.)

*Summary: Dagger can be used with or without a PaaS system.*

A PaaS system is a complete platform for deploying and running certain types of applications.

* The benefit of using a PaaS system is convenience: developers don't have to worry about the many details of deployment: everything just works.
* The drawback of using a PaaS system is lack of flexibility: only certain types of applications are supported.

As an application grows, it is almost certain to outgrow the capabilities of a PaaS system, leaving no choice but to look for alternatives. A good strategy is to choose the right platform for each component. Some components continue to run on a PaaS system; others run on specialized infrastructure. This strategy can be implemented with Dagger: each component gets its own deployment plan expressed as code, and Dagger glues it all together into a single workflow.


## Dagger vs. artisanal deploy scripts

*Summary: Dagger can augment your deploy scripts, and later help you simplify or even remove them.*

Most applications don't fit entirely in any major PaaS system. Instead they are deployed by a patchwork of tools, usually glued together by an artisanal script.

A deploy script may be written in virtually any scripting language. The most commonly used languages include Bash, Powershell, Make, Python, Ruby, Javascript... As well as a plethora of niche specialized languages.

Most teams are unhappy with their deploy script. They are high maintenance, tend to break at the worst possible time, and are less convenient to use than a PaaS. But when you need control of your stack, what other choice is there?

Dagger can either replace artisanal deploy scripts altogether, or augment them by incorporating them into a more standardized deployment system. This is a good strategy for teams which already have scripts and want to improve their deployment gradually, without the disruption of a "big bang" rewrite.
