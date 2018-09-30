const page = {
    nodes: {},
    init: function(name) {
        base.init(this.websocketFunc, function() {
            asticode.tools.sendHttp({
                method: "GET",
                url: "/api/workflows/" + name,
                error: base.defaultHttpError,
                success: function(data) {
                    // Store workflow name
                    page.workflow = name

                    // Init network
                    page.initNetwork(data.responseJSON)

                    // Init job
                    page.initJob(data.responseJSON)

                    // Hide loading
                    asticode.loader.hide()
                }
            })

        })
    },
    initNetwork: function(data) {
        // Handle node click
        window.handleNodeClick = function(name) {
            // Get node
            const node = page.nodes[name]

            // Node doesn't exist
            if (typeof node === "undefined") return

            // Send order to API
            asticode.loader.show()
            asticode.tools.sendHttp({
                method: "GET",
                url: "/api/workflows/" + page.workflow + "/nodes/" + name + "/" + (node.status === "started" ? "stop" : "start"),
                error: base.defaultHttpError,
                success: base.defaultHttpSuccess,
            })
        }

        // Create graph description
        let desc = "graph TB\n"

        // Add nodes
        for (let idx = 0; idx < data.nodes.length; idx++) {
            // Get stats
            let stats = ""
            if (data.nodes[idx].stats.length > 0) {
                stats += "<br><br><table>"
                for (let idxStat = 0; idxStat < data.nodes[idx].stats.length; idxStat++) {
                    stats += "<tr><td>" + data.nodes[idx].stats[idxStat].label + ":</td><td><span></span>" + data.nodes[idx].stats[idxStat].unit + "</td>"
                }
                stats += "</table>"
            }

            // Add node graph description
            desc += "    " + data.nodes[idx].name + "(\"" + data.nodes[idx].label + stats + "\")\n"
            desc += "    class " + data.nodes[idx].name + " " + data.nodes[idx].status + ";"
            desc += "    click " + data.nodes[idx].name + " handleNodeClick;"

            // Add node to pool
            page.nodes[data.nodes[idx].name] = {
                status: data.nodes[idx].status
            }
        }

        // Add edges
        for (let idx = 0; idx < data.edges.length; idx++) {
            desc += "    " + data.edges[idx].from + "-->" + data.edges[idx].to + "\n"
        }

        // Add graph description
        document.getElementById("network").innerHTML = desc

        // Initialize mermaid
        mermaid.init({}, ".network")
    },
    initJob: function(data) {
        document.getElementById("job").innerText = JSON.stringify(data.job, null, 4)
    },
    websocketFunc: function(eventName, payload) {
        switch (eventName) {
            case "node.started":
            case "node.stopped":
                // Get node
                const node = document.getElementById(payload)

                // Node doesn't exist
                if (typeof node === "undefined") return

                // Update class
                asticode.tools.removeClass(node, eventName === "node.started" ? "stopped" : "started")
                asticode.tools.addClass(node, eventName === "node.started" ? "started" : "stopped")

                // Update status
                if (typeof page.nodes[payload] !== "undefined") page.nodes[payload].status = (eventName === "node.started" ? "started" : "stopped")
                break
            case "stats":
                // Get element
                const el = document.getElementById(payload.name)

                // Element doesn't exist
                if (typeof el === "undefined") return

                // Get lines
                const trs = el.querySelectorAll("tr")

                // Loop through stats
                for (let idx = 0; idx < payload.stats.length; idx ++) {
                    // Line doesn't exist
                    if (trs.length <= idx) break

                    // Get rows
                    const tds = trs[idx].querySelectorAll("td")

                    // Not enough rows
                    if (tds.length < 2) continue

                    // Set value
                    tds[1].querySelector("span").innerText = payload.stats[idx].value.toFixed(2)
                }
                break
        }
    },
}