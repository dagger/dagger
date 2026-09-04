------------------------- MODULE NestedClientProxy -------------------------
(***************************************************************************)
(* A TLC-checked refinement of the process-level proxy that an engine-owned *)
(* exec exposes to nested clients. ClientLifecycle.tla owns the server-side *)
(* lease/reclamation transition after a successful registration; this model *)
(* owns the identity and routing decision that precedes it.                  *)
(*                                                                           *)
(* Four identities are deliberately distinct here: the held executable      *)
(* scope, context metadata (which may name a workspace-owning ancestor), a   *)
(* request's logical client ID, and the exec bootstrap attachables carrier.  *)
(***************************************************************************)
EXTENDS FiniteSets, Naturals, TLC

CONSTANTS
    Clients,
    Base,
    NoClient,
    ScopeParent,
    MetadataParent,
    ForeignCarrier,
    ExecSecret,
    OtherSecret,
    Requests,
    UseMetadataParent,
    AllowCrossCarrier,
    AllowClosedRebind,
    AllowClosedSubstitution,
    AllowMalformedFallback,
    RetryClosedTransport,
    CloseAllOnOneClose

ASSUME /\ Base \in Clients
       /\ Cardinality(Clients) >= 3
       /\ NoClient \notin Clients
       /\ ForeignCarrier \notin Clients
       /\ ScopeParent # MetadataParent
       /\ ExecSecret # OtherSecret
       /\ UseMetadataParent \in BOOLEAN
       /\ AllowCrossCarrier \in BOOLEAN
       /\ AllowClosedRebind \in BOOLEAN
       /\ AllowClosedSubstitution \in BOOLEAN
       /\ AllowMalformedFallback \in BOOLEAN
       /\ RetryClosedTransport \in BOOLEAN
       /\ CloseAllOnOneClose \in BOOLEAN

TransportStates == {"absent", "open", "closed"}
RequestPhases == {"idle", "done"}
RequestKinds == {"none", "headerless", "header", "malformed"}
RequestResults == {"none", "served", "bad-request", "conflict", "gone", "retry"}
Parents == {ScopeParent, MetadataParent, NoClient}
Carriers == Clients \cup {ForeignCarrier, NoClient}
Secrets == {ExecSecret, OtherSecret, NoClient}

EmptyRequest ==
    [phase |-> "idle",
     kind |-> "none",
     expected |-> NoClient,
     servedAs |-> NoClient,
     result |-> "none",
     observedClosed |-> FALSE]

RequestResult(kind, expected, servedAs, result, observedClosed) ==
    [phase |-> "done",
     kind |-> kind,
     expected |-> expected,
     servedAs |-> servedAs,
     result |-> result,
     observedClosed |-> observedClosed]

VARIABLES
    proxyOpen,
    carrierOpen,
    transport,
    parent,
    carrier,
    secret,
    everClosed,
    closeRequested,
    requests

vars == <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
          everClosed, closeRequested, requests>>

Registered == {c \in Clients : transport[c] # "absent"}
OpenClients == {c \in Clients : transport[c] = "open"}

Init ==
    /\ proxyOpen = TRUE
    /\ carrierOpen = FALSE
    /\ transport = [c \in Clients |-> "absent"]
    /\ parent = [c \in Clients |-> NoClient]
    /\ carrier = [c \in Clients |-> NoClient]
    /\ secret = [c \in Clients |-> NoClient]
    /\ everClosed = {}
    /\ closeRequested = {}
    /\ requests = [r \in Requests |-> EmptyRequest]

(***************************************************************************)
(* BOOTSTRAP AND EXACT REQUEST ROUTING                                     *)
(***************************************************************************)

\* Go: /.init explicitly stamps the exec-created base ID. The manager
\* registers that exact transport before accepting logical child clients.
Bootstrap ==
    /\ proxyOpen
    /\ ~carrierOpen
    /\ transport[Base] = "absent"
    /\ carrierOpen' = TRUE
    /\ transport' = [transport EXCEPT ![Base] = "open"]
    /\ parent' = [parent EXCEPT
         ![Base] = IF UseMetadataParent THEN MetadataParent ELSE ScopeParent]
    /\ carrier' = [carrier EXCEPT ![Base] = Base]
    /\ secret' = [secret EXCEPT ![Base] = ExecSecret]
    /\ UNCHANGED <<proxyOpen, everClosed, closeRequested, requests>>

\* Go: a header-aware request may create exactly its declared logical ID,
\* but only while the exact bootstrap transport is live. All authority and
\* capability fields come from the process proxy, not the request metadata.
RegisterHeader(r, c) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ c # Base
    /\ transport[Base] = "open"
    /\ transport[c] = "absent"
    /\ transport' = [transport EXCEPT ![c] = "open"]
    /\ parent' = [parent EXCEPT
         ![c] = IF UseMetadataParent THEN MetadataParent ELSE ScopeParent]
    /\ carrier' = [carrier EXCEPT
         ![c] = IF AllowCrossCarrier THEN ForeignCarrier ELSE Base]
    /\ secret' = [secret EXCEPT
         ![c] = IF AllowCrossCarrier THEN OtherSecret ELSE ExecSecret]
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("header", c, c, "served", FALSE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, everClosed, closeRequested>>

\* Headerless calls are a separate protocol and use only the one base ID
\* explicitly delegated to this exec.
ServeHeaderless(r) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ transport[Base] = "open"
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("headerless", Base, Base, "served", FALSE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

ServeHeader(r, c) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ transport[c] = "open"
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("header", c, c, "served", FALSE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

RejectMalformed(r) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ requests' = [requests EXCEPT
         ![r] = IF AllowMalformedFallback /\ transport[Base] = "open"
                THEN RequestResult("malformed", NoClient, Base,
                                   "served", FALSE)
                ELSE RequestResult("malformed", NoClient, NoClient,
                                   "bad-request", FALSE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

RejectWithoutBootstrap(r, c) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ c # Base
    /\ transport[c] = "absent"
    /\ transport[Base] # "open"
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("header", c, NoClient, "conflict", FALSE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

(***************************************************************************)
(* TERMINAL TRANSPORTS AND DELIBERATE MUTATION ACTIONS                     *)
(***************************************************************************)

ServeClosed(r, kind, c) ==
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ kind \in {"headerless", "header"}
    /\ (kind = "headerless" => c = Base)
    /\ transport[c] = "closed"
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult(kind, c, NoClient,
                  IF RetryClosedTransport THEN "retry" ELSE "gone", TRUE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

\* Deliberately defective mutation: reopening a tombstoned ID. The rebind
\* configuration must demonstrate that NoClosedIDRebind detects it.
RebindClosed(r, c) ==
    /\ AllowClosedRebind
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ transport[c] = "closed"
    /\ transport' = [transport EXCEPT ![c] = "open"]
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("header", c, c, "served", TRUE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, parent, carrier, secret,
                    everClosed, closeRequested>>

\* Deliberately defective mutation: serving a closed requested ID as some
\* other live client. ExactRouting must detect any such substitution.
SubstituteClosed(r, expected, actual) ==
    /\ AllowClosedSubstitution
    /\ proxyOpen
    /\ requests[r].phase = "idle"
    /\ expected # actual
    /\ transport[expected] = "closed"
    /\ transport[actual] = "open"
    /\ requests' = [requests EXCEPT
         ![r] = RequestResult("header", expected, actual, "served", TRUE)]
    /\ UNCHANGED <<proxyOpen, carrierOpen, transport, parent, carrier, secret,
                    everClosed, closeRequested>>

CloseTransport(c) ==
    /\ proxyOpen
    /\ transport[c] = "open"
    /\ LET closing == IF CloseAllOnOneClose THEN OpenClients ELSE {c} IN
       /\ transport' = [x \in Clients |->
            IF x \in closing THEN "closed" ELSE transport[x]]
       /\ everClosed' = everClosed \cup closing
    /\ closeRequested' = closeRequested \cup {c}
    /\ UNCHANGED <<proxyOpen, carrierOpen, parent, carrier, secret, requests>>

\* Exec cleanup closes every still-open transport and the bootstrap carrier
\* exactly as one process-level terminal transition.
CloseProxy ==
    /\ proxyOpen
    /\ proxyOpen' = FALSE
    /\ carrierOpen' = FALSE
    /\ transport' = [c \in Clients |->
         IF transport[c] = "open" THEN "closed" ELSE transport[c]]
    /\ everClosed' = everClosed \cup OpenClients
    /\ UNCHANGED <<parent, carrier, secret, closeRequested, requests>>

Next ==
    \/ Bootstrap
    \/ \E r \in Requests, c \in Clients : RegisterHeader(r, c)
    \/ \E r \in Requests : ServeHeaderless(r)
    \/ \E r \in Requests, c \in Clients : ServeHeader(r, c)
    \/ \E r \in Requests : RejectMalformed(r)
    \/ \E r \in Requests, c \in Clients : RejectWithoutBootstrap(r, c)
    \/ \E r \in Requests, c \in Clients,
          kind \in {"headerless", "header"} : ServeClosed(r, kind, c)
    \/ \E r \in Requests, c \in Clients : RebindClosed(r, c)
    \/ \E r \in Requests, expected \in Clients, actual \in Clients :
          SubstituteClosed(r, expected, actual)
    \/ \E c \in Clients : CloseTransport(c)
    \/ CloseProxy

Spec == Init /\ [][Next]_vars

(***************************************************************************)
(* SAFETY PROPERTIES                                                       *)
(***************************************************************************)

TypeOK ==
    /\ proxyOpen \in BOOLEAN
    /\ carrierOpen \in BOOLEAN
    /\ DOMAIN transport = Clients
    /\ DOMAIN parent = Clients
    /\ DOMAIN carrier = Clients
    /\ DOMAIN secret = Clients
    /\ DOMAIN requests = Requests
    /\ \A c \in Clients :
         /\ transport[c] \in TransportStates
         /\ parent[c] \in Parents
         /\ carrier[c] \in Carriers
         /\ secret[c] \in Secrets
    /\ everClosed \subseteq Clients
    /\ closeRequested \subseteq Clients
    /\ \A r \in Requests :
         /\ requests[r].phase \in RequestPhases
         /\ requests[r].kind \in RequestKinds
         /\ requests[r].expected \in Clients \cup {NoClient}
         /\ requests[r].servedAs \in Clients \cup {NoClient}
         /\ requests[r].result \in RequestResults
         /\ requests[r].observedClosed \in BOOLEAN

\* Context metadata may name an ancestor for workspace/schema ownership, but
\* only the held executable scope may authorize the server-side child edge.
ParentUsesScope ==
    \A c \in Registered : parent[c] = ScopeParent

\* Every logical transport is sealed to the one exec bootstrap carrier. The
\* carrier and logical record have the same parent authority and secret.
CarrierBindingExact ==
    /\ Base \in Registered =>
         /\ carrier[Base] = Base
         /\ secret[Base] = ExecSecret
    /\ \A c \in Registered \ {Base} :
         /\ carrier[c] = Base
         /\ parent[c] = parent[Base]
         /\ secret[c] = secret[Base]

\* A successful route always serves the exact syntactically selected ID.
ExactRouting ==
    \A r \in Requests :
        requests[r].phase = "done" /\ requests[r].result = "served"
          => requests[r].servedAs = requests[r].expected

MalformedRejected ==
    \A r \in Requests :
        requests[r].phase = "done" /\ requests[r].kind = "malformed"
          => /\ requests[r].result = "bad-request"
             /\ requests[r].servedAs = NoClient

\* A client ID is a one-shot capability and cannot leave the closed state.
NoClosedIDRebind ==
    \A c \in everClosed : transport[c] = "closed"

\* Requests that observe a closed transport receive a terminal result. A
\* retryable response would permit the SSE hang that motivated this model.
ClosedIsTerminal ==
    \A r \in Requests :
        requests[r].phase = "done" /\ requests[r].observedClosed
          => requests[r].result = "gone"

\* Closing one logical transport cannot implicitly close any sibling. Proxy
\* cleanup is the only transition allowed to close all remaining transports.
IndependentClose ==
    \A c \in Clients :
        proxyOpen /\ transport[c] = "closed" => c \in closeRequested

ProxyCloseTerminal ==
    ~proxyOpen => /\ ~carrierOpen
                  /\ OpenClients = {}

=============================================================================
