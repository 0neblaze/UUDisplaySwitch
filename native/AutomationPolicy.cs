namespace UUDisplaySwitch;

// The completion timestamp alone is not an instruction to close the application.
public static class AutomationPolicy
{
    public static bool ShouldExit(Prefs preferences, CoreState state, long enabledAfter) =>
        preferences.AutoExit && state.UuStateKnown && !state.UuRunning && !state.UuUpdating &&
        !state.WatcherActive && state.WatcherStatus != "error" && state.UuExitAt > enabledAfter &&
        (!preferences.AutoDisable || state.Status == "disabled");

    public static void Validate()
    {
        var preferences = new Prefs { AutoExit = true, AutoDisable = true };
        var state = new CoreState { Status = "disabled", UuStateKnown = true, UuExitAt = 101 };
        void Require(bool condition,string name){if(!condition)throw new InvalidOperationException("Automation regression: "+name);}
        Require(ShouldExit(preferences,state,100),"confirmed completion");
        state.UuRunning=true;Require(!ShouldExit(preferences,state,100),"UU still running");state.UuRunning=false;
        state.UuUpdating=true;Require(!ShouldExit(preferences,state,100),"UU updating");state.UuUpdating=false;
        state.UuStateKnown=false;Require(!ShouldExit(preferences,state,100),"unknown UU state");state.UuStateKnown=true;
        state.WatcherActive=true;Require(!ShouldExit(preferences,state,100),"monitor still active");state.WatcherActive=false;
        state.WatcherStatus="error";Require(!ShouldExit(preferences,state,100),"monitor failure");state.WatcherStatus="stopped";
        state.Status="enabled";Require(!ShouldExit(preferences,state,100),"adapter disable failed");
        preferences.AutoDisable=false;Require(ShouldExit(preferences,state,100),"exit only");
        Require(!ShouldExit(preferences,state,101),"old event after option toggled");
        preferences.AutoExit=false;Require(!ShouldExit(preferences,state,100),"exit not opted in");
    }
}
