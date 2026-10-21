package com.github.kr328.clash

import com.github.kr328.clash.common.util.intent
import com.github.kr328.clash.core.Clash
import com.github.kr328.clash.design.ProxyDesign
import com.github.kr328.clash.design.model.ProxyState
import com.github.kr328.clash.util.withClash
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.selects.select
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit

class ProxyActivity : BaseActivity<ProxyDesign>() {
    override suspend fun main() {
        val mode = withClash { queryOverride(Clash.OverrideSlot.Session).mode }
        val names = filterVisibleGroups(
            withClash { queryProxyGroupNames(uiStore.proxyExcludeNotSelectable) }
        )
        val states = List(names.size) { ProxyState("?") }
        val unorderedStates = names.indices.map { names[it] to states[it] }.toMap()
        val reloadLock = Semaphore(10)

        val design = ProxyDesign(
            this,
            mode,
            names,
            uiStore
        )

        setContentDesign(design)

        design.requests.send(ProxyDesign.Request.ReloadAll)

        while (isActive) {
            select<Unit> {
                events.onReceive {
                    when (it) {
                        Event.ProfileLoaded -> {
                            val newNames = filterVisibleGroups(
                                withClash {
                                    queryProxyGroupNames(uiStore.proxyExcludeNotSelectable)
                                }
                            )

                            if (newNames != names) {
                                startActivity(ProxyActivity::class.intent)

                                finish()
                            } else {
                                // Group identity unchanged (e.g. subscription refresh):
                                // re-query `now` so the auto-select switch tracks reality.
                                design.requests.trySend(ProxyDesign.Request.ReloadAll)
                            }
                        }
                        else -> Unit
                    }
                }
                design.requests.onReceive {
                    when (it) {
                        ProxyDesign.Request.ReLaunch -> {
                            startActivity(ProxyActivity::class.intent)

                            finish()
                        }
                        ProxyDesign.Request.ReloadAll -> {
                            names.indices.forEach { idx ->
                                design.requests.trySend(ProxyDesign.Request.Reload(idx))
                            }
                        }
                        is ProxyDesign.Request.Reload -> {
                            launch {
                                val group = reloadLock.withPermit {
                                    withClash {
                                        queryProxyGroup(names[it.index], uiStore.proxySort)
                                    }
                                }
                                val state = states[it.index]

                                state.now = group.now

                                // The auto url-test group is controlled by the
                                // toolbar switch, not by a list entry.
                                val proxies = group.proxies.filter { p ->
                                    p.name != AUTO_GROUP_NAME
                                }

                                design.updateGroup(
                                    it.index,
                                    proxies,
                                    group.type == "Selector",
                                    state,
                                    unorderedStates
                                )

                                if (names[it.index] == MANUAL_GROUP_NAME) {
                                    design.setAutoSelect(group.now == AUTO_GROUP_NAME)
                                }
                            }
                        }
                        is ProxyDesign.Request.Select -> {
                            val ok = withClash {
                                patchSelector(names[it.index], it.name)
                            }

                            if (ok) {
                                states[it.index].now = it.name

                                if (names[it.index] == MANUAL_GROUP_NAME) {
                                    design.setAutoSelect(it.name == AUTO_GROUP_NAME)
                                }
                            }

                            design.requestRedrawVisible()
                        }
                        is ProxyDesign.Request.ToggleAuto -> {
                            if (it.enabled) {
                                val ok = withClash {
                                    patchSelector(MANUAL_GROUP_NAME, AUTO_GROUP_NAME)
                                }

                                if (ok) {
                                    design.setAutoSelect(true)

                                    design.requests.trySend(ProxyDesign.Request.ReloadAll)
                                } else {
                                    design.setAutoSelect(false)
                                }
                            } else {
                                // Unlock the list only; the exit stays on the auto
                                // group until the user picks a node.
                                design.setAutoSelect(false)
                            }
                        }
                        is ProxyDesign.Request.UrlTest -> {
                            launch {
                                withClash {
                                    healthCheck(names[it.index])
                                }

                                design.requests.send(ProxyDesign.Request.Reload(it.index))
                            }
                        }
                        is ProxyDesign.Request.PatchMode -> {
                            design.showModeSwitchTips()

                            withClash {
                                val o = queryOverride(Clash.OverrideSlot.Session)

                                o.mode = it.mode

                                patchOverride(Clash.OverrideSlot.Session, o)
                            }
                        }
                    }
                }
            }
        }
    }

    companion object {
        private const val AUTO_GROUP_NAME = "MihomoOne"
        private const val MANUAL_GROUP_NAME = "节点选择"

        // 内核里其它分组仍被数千条分流规则引用，不能删；这里只显示手动控制组。
        // 找不到「节点选择」时显示空态并禁用开关，不把别的组误当主出口。
        private fun filterVisibleGroups(names: List<String>): List<String> {
            return if (MANUAL_GROUP_NAME in names) listOf(MANUAL_GROUP_NAME) else emptyList()
        }
    }
}
