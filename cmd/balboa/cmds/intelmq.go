// balboa
// Copyright (c) 2019, DCSO GmbH

package cmds

import (
	db "github.com/DCSO/balboa/backend/go"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var intelMqCmd = &cobra.Command{
	Use:        "intelmq [intelmq host] [intelmq tcp listener port]",
	Aliases:    nil,
	SuggestFor: nil,
	Short:      "Start a balboa backend service which relays observations to an IntelMQ TCP collector",
	Long: `This command will start a balboa backend which receives observations from the balboa
 frontend and relays them to the specified IntelMQ TCP collector.`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		intelMqCollector, err := cmd.Flags().GetString("intelmq-tcp-collector")
		if err != nil {
			log.Fatal(err)
		}
		host, err := cmd.Flags().GetString("host")
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("running IntelMQ relay backend and relaying events to %s", intelMqCollector)

		db.Serve(host, db.NewIntelMqHandler(intelMqCollector))
	},
}

func init() {
	rootCmd.AddCommand(intelMqCmd)

	intelMqCmd.Flags().StringP("intelmq-tcp-collector", "s", "localhost:5123", "hostname and port of IntelMQ TCP collector")
	intelMqCmd.Flags().StringP("host", "H", "localhost:4242", "listen host and port of the backend")
}
