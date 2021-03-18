# What is Application Delivery as Code?

## The problem with PaaS

A PaaS, or platform-as-a-service, is the glue between an application and the cloud infrastructure running it. It automates the deployment process and provides a simplified view of the underlying infrastructure, which makes developers more productive.

However, despite the undeniable productivity benefits of using a PaaS, most applications today do not. Why? Because it's not flexible enough: each PaaS only supports certain types of application stacks and infrastructure. Applications that cannot adapt to the platform are simply not supported.


## The problem with artisanal deploy scripts

Most applications don't fit in any major PaaS. Instead they are deployed by a patchwork of specialized tools, usually glued together by an artisanal shell script or equivalent.

*FIXME: example of specialized tools*

Most teams are unhappy with their deploy script. They are high maintenance, tend to break at the worst possible time, and are less convenient to use than a PaaS. But when you need control of your stack, what other choice is there?


## The best of both worlds: Application Delivery as Code

Application Delivery as Code (ADC) is an alternative to PaaS and artisanal deploy scripts, which combines the best of each.

Simply put, with ADC each application gets its own single-purpose PaaS. The platform adapts to the application, not the other way around. And because it’s defined *as code*, this custom PaaS can easily be changed over time, as the application stack and infrastructure evolves.

Because doing this with a general programming language, like Go, would be prohibitively expensive, a good ADC implementation requires a specialized language and runtime environment. For example, Dagger uses [Cue](https://cuelang.org) as its language, and [Buildkit](https://github.com/moby/buildkit) as its runtime environment.
